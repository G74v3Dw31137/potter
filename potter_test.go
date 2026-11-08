package potter_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	mu              sync.Mutex
	page, script    []byte
	visitor         string
	integrityReply  string
	status          map[string]int
	calls           map[string]int
	bodiesClosed    int
	transportClosed bool
	block, entered  chan struct{}
	once            sync.Once
}

func fixturePage(flags, visitor string) []byte {
	cfg := map[string]any{
		"INNERTUBE_CONTEXT": map[string]any{"client": map[string]any{
			"clientName": "MWEB", "clientVersion": "fixture-version", "visitorData": visitor,
		}},
		"WEB_PLAYER_CONTEXT_CONFIGS": map[string]any{"WEB_PLAYER_CONTEXT_CONFIG_ID_MWEB_WATCH": map[string]any{
			"serializedExperimentFlags": flags,
		}},
	}
	raw, _ := json.Marshal(cfg)
	return []byte(`<script>ytcfg.set(` + string(raw) + `);window.ytAtN({R:{bgChallenge:{program:'fixture-program',globalName:'testGuard',interpreterUrl:{privateDoNotAccessOrElseTrustedResourceUrlWrappedValue:'https://www.google.com/botguard/fixture.js'}}}});</script>`)
}

func newFixture(t *testing.T, flags string) (*potter.Client, *fixture) {
	t.Helper()
	script, err := os.ReadFile("internal/botguard/testdata/protocol-mock.js")
	require.NoError(t, err)
	f := &fixture{page: fixturePage(flags, "fixture-visitor"), visitor: "fixture-visitor", script: script, status: map[string]int{}, calls: map[string]int{}}
	c, err := potter.NewClient(potter.WithHTTPClient(&http.Client{Transport: f}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c, f
}

type trackedBody struct {
	io.Reader
	close func()
}

func (b *trackedBody) Close() error { b.close(); return nil }

func (f *fixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	kind := ""
	var raw []byte
	switch {
	case r.Method == "GET" && r.URL.String() == "https://m.youtube.com/":
		kind, raw = "homepage", f.page
	case r.Method == "GET" && r.URL.String() == "https://www.google.com/botguard/fixture.js":
		kind, raw = "interpreter", f.script
	case r.Method == "POST" && r.URL.String() == "https://jnn-pa.googleapis.com/$rpc/google.internal.waa.v1.Waa/GenerateIT":
		kind, raw = "integrity", []byte(`["VEVTVElU",600,30]`)
		if f.integrityReply != "" {
			raw = []byte(f.integrityReply)
		}
	default:
		f.mu.Unlock()
		return nil, errors.New("unexpected fixture request")
	}
	f.calls[kind]++
	status := f.status[kind]
	visitor := f.visitor
	f.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	if r.Header.Get("User-Agent") != potter.UserAgent {
		return nil, errors.New("wrong user agent")
	}
	if kind != "homepage" && r.Header.Get("X-Goog-Visitor-Id") != visitor {
		return nil, errors.New("wrong visitor header")
	}
	if kind == "integrity" {
		var body []string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 2 || body[0] != "O43z0dpjhgX20SCx4KAo" || body[1] != "fixture-snapshot:fixture-program" {
			return nil, errors.New("wrong attestation payload")
		}
		if r.Header.Get("Content-Type") != "application/json+protobuf" || r.Header.Get("X-User-Agent") != "grpc-web-javascript/0.1" || r.Header.Get("X-Goog-Api-Key") == "" {
			return nil, errors.New("wrong attestation headers")
		}
		if f.block != nil {
			f.once.Do(func() { close(f.entered) })
			select {
			case <-f.block:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: &trackedBody{Reader: bytes.NewReader(raw), close: func() { f.mu.Lock(); f.bodiesClosed++; f.mu.Unlock() }}, Request: r}, nil
}

func (f *fixture) CloseIdleConnections() { f.mu.Lock(); f.transportClosed = true; f.mu.Unlock() }

func decode(t *testing.T, token string) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(token, "="))
	require.NoError(t, err)
	return string(raw)
}

func TestTokensBindingsAndOriginalCallback(t *testing.T) {
	for _, flags := range []string{"html5_generate_content_po_token=true", "html5_generate_content_po_token=false", "", "html5_generate_content_po_token=false&html5_generate_content_po_token=true"} {
		t.Run(flags, func(t *testing.T) {
			c, f := newFixture(t, flags)
			assert.Empty(t, f.calls, "constructor performed HTTP")
			require.NoError(t, c.Warmup(context.Background()))
			binding := "fixture-visitor"
			if strings.HasSuffix(flags, "=true") {
				binding = "video-a"
			}
			a, err := c.Tokens(context.Background(), "video-a")
			require.NoError(t, err)
			assert.Equal(t, "fixture:1:video-a", decode(t, a.Player), "original callback order/binding changed")
			assert.Equal(t, "fixture:2:"+binding, decode(t, a.GVS), "original callback order/binding changed")
			assert.Equal(t, binding, a.GVSBinding, "wrong identity metadata")
			assert.Equal(t, "fixture-visitor", a.VisitorData, "wrong identity metadata")
			assert.Equal(t, "fixture-version", a.ClientVersion, "wrong identity metadata")
			again, err := c.Tokens(context.Background(), "video-a")
			require.NoError(t, err, "warm proof cache changed")
			assert.True(t, a == again, "warm proof cache changed")
			b, err := c.Tokens(context.Background(), "video-b")
			require.NoError(t, err, "Player binding/cache changed")
			assert.Equal(t, "fixture:3:video-b", decode(t, b.Player), "Player binding/cache changed")
			if binding == "fixture-visitor" {
				assert.Equal(t, a.GVS, b.GVS, "visitor GVS cache changed")
			} else {
				assert.Equal(t, "fixture:4:video-b", decode(t, b.GVS), "video GVS binding changed")
			}
			require.NoError(t, c.Close())
			f.mu.Lock()
			defer f.mu.Unlock()
			assert.Equal(t, 1, f.calls["homepage"], "HTTP work/cleanup changed %v", f.calls)
			assert.Equal(t, 1, f.calls["interpreter"], "HTTP work/cleanup changed %v", f.calls)
			assert.Equal(t, 1, f.calls["integrity"], "HTTP work/cleanup changed %v", f.calls)
			assert.Equal(t, 3, f.bodiesClosed, "HTTP work/cleanup changed %v", f.calls)
			assert.False(t, f.transportClosed, "HTTP work/cleanup changed %v", f.calls)
		})
	}
}

func TestPublicErrors(t *testing.T) {
	for _, kind := range []string{"homepage", "interpreter", "integrity"} {
		t.Run(kind, func(t *testing.T) {
			c, f := newFixture(t, "html5_generate_content_po_token=true")
			f.status[kind] = 403
			proofs, err := c.Tokens(context.Background(), "video")
			want := map[string]error{"homepage": potter.ErrChallenge, "interpreter": potter.ErrInterpreter, "integrity": potter.ErrGenerateIT}[kind]
			var stage *potter.Error
			require.ErrorIs(t, err, want, "stage/status lost")
			require.ErrorAs(t, err, &stage, "stage/status lost")
			assert.Equal(t, 403, stage.StatusCode, "stage/status lost")
			assert.True(t, proofs == (potter.Tokens{}), "stage/status lost")
		})
	}
	c, f := newFixture(t, "html5_generate_content_po_token=true")
	{
		_, err := c.Tokens(context.Background(), "")
		require.ErrorIs(t, err, potter.ErrBinding, "empty binding caused HTTP")
		require.Empty(t, f.calls, "empty binding caused HTTP")
	}
	proofs, err := c.Tokens(context.Background(), "reject")
	require.ErrorIs(t, err, potter.ErrMint, "mint error leaked or fell back")
	assert.False(t, strings.Contains(err.Error(), "PRIVATE"), "mint error leaked or fell back")
	assert.True(t, proofs == (potter.Tokens{}), "mint error leaked or fell back")
	c, f = newFixture(t, "html5_generate_content_po_token=true")
	// GVS can fail after Player succeeds; no partial proofs may escape.
	f.page = fixturePage("html5_generate_content_po_token=false", "reject")
	f.visitor = "reject"
	proofs, err = c.Tokens(context.Background(), "video")
	require.ErrorIs(t, err, potter.ErrMint, "partial failure returned proofs")
	assert.True(t, proofs == (potter.Tokens{}), "partial failure returned proofs")
	c, f = newFixture(t, "html5_generate_content_po_token=true")
	f.integrityReply = `[null,600,null,"PRIVATE fallback"]`
	{
		_, err := c.Tokens(context.Background(), "video")
		require.ErrorIs(t, err, potter.ErrIntegrity, "invalid integrity accepted or leaked")
		assert.False(t, strings.Contains(err.Error(), "PRIVATE"), "invalid integrity accepted or leaked")
	}
}

// Ports the extractor's refresh/publication regression through Potter's API.
func TestRefreshPublishesCoherentIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.integrityReply = `["VEVTVElU",2,30]`
		_, err := c.Tokens(context.Background(), "video-a")
		require.NoError(t, err)
		f.mu.Lock()
		f.visitor = "next-visitor"
		f.page = fixturePage("html5_generate_content_po_token=false", f.visitor)
		f.mu.Unlock()
		// Advance beyond the fixture integrity TTL, then observe session replacement.
		timer := time.NewTimer(2*time.Second + 10*time.Millisecond)
		defer timer.Stop()
		<-timer.C
		b, err := c.Tokens(context.Background(), "video-b")
		require.NoError(t, err, "refreshed identity not adopted")
		assert.Equal(t, "next-visitor", b.VisitorData, "refreshed proofs and identity are incoherent")
		assert.Equal(t, b.VisitorData, b.GVSBinding, "refreshed proofs and identity are incoherent")
		assert.Equal(t, "fixture:1:video-b", decode(t, b.Player), "refreshed proofs and identity are incoherent")
		assert.Equal(t, "fixture:2:next-visitor", decode(t, b.GVS), "refreshed proofs and identity are incoherent")
		f.mu.Lock()
		defer f.mu.Unlock()
		assert.Equal(t, 2, f.calls["homepage"], "refresh duplicated work %v", f.calls)
		assert.Equal(t, 2, f.calls["integrity"], "refresh duplicated work %v", f.calls)
		assert.Equal(t, 1, f.calls["interpreter"], "refresh duplicated work %v", f.calls)
	})
}

func TestBindingConfigurationFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(string) string
		stage  error
		code   string
	}{
		{"flag-value", func(s string) string { return strings.Replace(s, "=true", "=unknown", 1) }, potter.ErrBinding, "invalid-watch-config"},
		{"flag-encoding", func(s string) string { return strings.Replace(s, "=true", "=%ZZ", 1) }, potter.ErrBinding, "invalid-watch-config"},
		{"missing-watch", func(s string) string {
			return strings.Replace(s, "WEB_PLAYER_CONTEXT_CONFIG_ID_MWEB_WATCH", "OTHER", 1)
		}, potter.ErrBinding, "invalid-watch-config"},
		{"logged-in", func(s string) string {
			return strings.Replace(s, `"INNERTUBE_CONTEXT":`, `"LOGGED_IN":true,"INNERTUBE_CONTEXT":`, 1)
		}, potter.ErrChallenge, "logged-in"},
		{"visitor-mismatch", func(s string) string {
			return strings.Replace(s, `"INNERTUBE_CONTEXT":`, `"VISITOR_DATA":"different","INNERTUBE_CONTEXT":`, 1)
		}, potter.ErrChallenge, "invalid-visitor-data"},
		{"wrong-client", func(s string) string { return strings.Replace(s, `"clientName":"MWEB"`, `"clientName":"WEB"`, 1) }, potter.ErrChallenge, "invalid-client-config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFixture(t, "html5_generate_content_po_token=true")
			f.page = []byte(tc.change(string(f.page)))
			_, err := c.Tokens(context.Background(), "video")
			require.ErrorIs(t, err, tc.stage, "invalid binding accepted")
			var classified *potter.Error
			require.ErrorAs(t, err, &classified)
			require.Equal(t, tc.code, classified.Code)
			require.Equal(t, 0, f.calls["integrity"], "invalid binding reached initialization")
			require.Equal(t, 0, f.calls["interpreter"], "invalid binding reached initialization")
		})
	}
}

func TestCloseCancelsBuildAndQueuedCalls(t *testing.T) {
	c, f := newFixture(t, "html5_generate_content_po_token=true")
	f.block, f.entered = make(chan struct{}), make(chan struct{})
	done := make(chan error, 2)
	go func() { done <- c.Warmup(context.Background()) }()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("build did not reach integrity")
	}
	go func() { _, err := c.Tokens(context.Background(), "video"); done <- err }()
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join workers")
	}
	for range 2 {
		require.Error(t, <-done, "closed operation succeeded")
	}
	require.NoError(t, c.Close())
	require.ErrorIs(t, c.Warmup(context.Background()), potter.ErrClosed)
	{
		_, err := c.Tokens(context.Background(), "video")
		require.ErrorIs(t, err, potter.ErrClosed)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	require.False(t, f.transportClosed, "caller transport closed")
}

func TestCancellationAndConcurrentUsage(t *testing.T) {
	c, f := newFixture(t, "html5_generate_content_po_token=true")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	{
		err := c.Warmup(ctx)
		require.ErrorIs(t, err, context.Canceled, "pre-cancellation lost")
		require.Empty(t, f.calls, "pre-cancellation lost")
	}
	f.block, f.entered = make(chan struct{}), make(chan struct{})
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Warmup(ctx) }()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("build not started")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	close(f.block)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			_, err := c.Tokens(context.Background(), fmt.Sprintf("video-%d", i))
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, 1, f.calls["integrity"], "canceled waiter destroyed shared initialization")
}

func TestNilHTTPClient(t *testing.T) {
	{
		_, err := potter.NewClient(potter.WithHTTPClient(nil))
		require.Error(t, err, "nil HTTP client accepted")
	}
}
