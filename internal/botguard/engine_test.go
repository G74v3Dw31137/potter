package botguard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeGoogle struct {
	script                      []byte
	gets, creates, attestations atomic.Int64
	version                     atomic.Int64
	status                      atomic.Int64
	integrity                   atomic.Value
	block, entered              chan struct{}
	once                        sync.Once
}

func fixtureContext() Context {
	return Context{Page: mockPage(1), Key: "identity-a", Revision: 1, CreatedAt: time.Now(), Visitor: "fixture-visitor", UserAgent: "fixture-UA", Binding: VideoBinding, Config: json.RawMessage(`{"INNERTUBE_CONTEXT":{"client":{"clientName":"MWEB","visitorData":"fixture-visitor"}}}`)}
}
func newFake(t *testing.T) (*Engine, *fakeGoogle) {
	t.Helper()
	script, e := os.ReadFile("testdata/protocol-mock.js")
	require.NoError(t, e)
	f := &fakeGoogle{script: script}
	f.version.Store(1)
	f.integrity.Store(`["VEVTVElU",60,10]`)
	m := NewEngine(&http.Client{Transport: f})
	t.Cleanup(m.Close)
	return m, f
}

// Exercise execution directly; no hidden initialization or lifecycle policy.
func createFixture(m *Engine, ctx context.Context, c Context) error {
	s, e := m.Create(ctx, c, time.Now)
	if s != nil {
		s.Close()
	}
	return e
}
func (f *fakeGoogle) RoundTrip(r *http.Request) (*http.Response, error) {
	var raw []byte
	status := 200
	switch {
	case r.Method == "GET" && r.URL.Path == "/":
		f.creates.Add(1)
		raw = mockPage(f.version.Load())
	case r.Method == "GET":
		f.gets.Add(1)
		raw = f.script
	case strings.HasSuffix(r.URL.Path, "/GenerateIT"):
		f.attestations.Add(1)
		if f.block != nil {
			f.once.Do(func() { close(f.entered) })
			select {
			case <-f.block:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		raw = []byte(f.integrity.Load().(string))
		if s := f.status.Load(); s != 0 {
			status = int(s)
		}
	default:
		status = 404
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(raw)), Request: r}, nil
}
func TestIntegrityRejectsFallbackAndInvalidTTL(t *testing.T) {
	for _, raw := range []string{`[null,43200,null,"FALLBACK"]`, `["",60]`, `["secret",0]`, `["secret",-1]`, `["secret",1e100]`, `[123,60]`, `{}`} {
		_, _, _, e := parseIntegrity([]byte(raw))
		require.ErrorIs(t, e, ErrIntegrity, "invalid integrity accepted/leaked")
		require.NotContains(t, e.Error(), "secret", "invalid integrity accepted/leaked")
		require.NotContains(t, e.Error(), "FALLBACK", "invalid integrity accepted/leaked")
	}
	m, f := newFake(t)
	f.integrity.Store(`[null,60,null,"PRIVATE fallback"]`)
	require.ErrorIs(t, createFixture(m, context.Background(), fixtureContext()), ErrIntegrity)
}
func TestScrollFixAndRuntimeRedaction(t *testing.T) {
	m, _ := newFake(t)
	programs, e := m.staticPrograms(context.Background())
	require.NoError(t, e)
	c := fixtureContext()
	rt := newRuntime(c)
	e = rt.initialize(context.Background(), c, programs)
	require.NoError(t, e)
	v, e := rt.vm.RunString(`(function(){var a=document.createElement('img'),b=document.createElement('img');if(a.scrollLeft!==0||b.scrollLeft!==0)return false;a.scrollLeft='2.5';return a.scrollLeft===2.5&&b.scrollLeft===0})()`)
	require.NoError(t, e, "scrollLeft regression")
	require.True(t, v.ToBoolean(), "scrollLeft regression")
	handle, e := m.Create(context.Background(), c, time.Now)
	require.NoError(t, e)
	defer handle.Close()
	_, e = handle.Mint(context.Background(), "reject")
	require.ErrorIs(t, e, ErrMint, "mint error leaked")
	require.NotContains(t, e.Error(), "PRIVATE", "mint error leaked")
}
func mockPage(version int64) []byte {
	payload := map[string]any{"R": map[string]any{"bgChallenge": map[string]any{"program": "fixture-program", "globalName": "testGuard", "interpreterHash": "version-" + string(rune('0'+version)), "interpreterUrl": map[string]string{"privateDoNotAccessOrElseTrustedResourceUrlWrappedValue": "https://www.google.com/botguard/interpreter-v" + string(rune('0'+version)) + ".js"}}}}
	raw, _ := json.Marshal(payload)
	return []byte("<script>window.ytAtN(" + string(raw) + ")</script>")
}

func TestImageAndNamespaceState(t *testing.T) {
	m, _ := newFake(t)
	programs, e := m.staticPrograms(context.Background())
	require.NoError(t, e)
	rt := newRuntime(fixtureContext())
	e = rt.initialize(context.Background(), fixtureContext(), programs)
	require.NoError(t, e)
	value, e := rt.vm.RunString(`(function(){
 var image=document.createElement('img');
 if(image.currentSrc!==''||typeof image.lookupPrefix!=='function')return false;
 image.src='https://not-loaded.invalid/image.png';if(image.currentSrc!=='')return false;
 var d=Object.getOwnPropertyDescriptor(LD.HTMLImageElement.prototype,'currentSrc');if(d.set!==undefined)return false;
 if(image.lookupPrefix(null)!==null||image.lookupPrefix('urn:missing')!==null||document.lookupPrefix('http://www.w3.org/1999/xhtml')!==null)return false;
 try{image.lookupPrefix();return false}catch(e){if(!(e instanceof TypeError))return false}
 return true;
 })()`)
	require.NoError(t, e, "unloaded image / namespace semantics failed")
	require.True(t, value.ToBoolean(), "unloaded image / namespace semantics failed")
}
func TestSessionOwnerControlsCleanup(t *testing.T) {
	engine, _ := newFake(t)
	handle, err := engine.Create(context.Background(), fixtureContext(), time.Now)
	require.NoError(t, err)
	// Compiled-source storage and a transferred runtime have separate owners.
	engine.Close()
	token, err := handle.Mint(context.Background(), "video")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	handle.Close()
	handle.Close()
	require.Nil(t, handle.rt)
	token, err = handle.Mint(context.Background(), "video")
	require.ErrorIs(t, err, ErrClosed)
	require.Empty(t, token)
}
