package potter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/G74v3Dw31137/potter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only explicitly enabled runs may contact Google. This transport forbids Player
// and media requests, redirects, and a second initialization attempt on the wire.
type liveBudget struct {
	base                             http.RoundTripper
	mu                               sync.Mutex
	homepage, interpreter, integrity int
}

func (b *liveBudget) RoundTrip(r *http.Request) (*http.Response, error) {
	b.mu.Lock()
	allowed := false
	switch {
	case r.Method == "GET" && r.URL.String() == "https://m.youtube.com/":
		b.homepage++
		allowed = b.homepage <= 1
	case r.Method == "POST" && r.URL.String() == "https://jnn-pa.googleapis.com/$rpc/google.internal.waa.v1.Waa/GenerateIT":
		b.integrity++
		allowed = b.integrity <= 1
	case r.Method == "GET" && r.URL.Scheme == "https" && r.URL.User == nil && r.URL.Path != "/" &&
		(r.URL.Host == "m.youtube.com" || r.URL.Host == "www.youtube.com" || r.URL.Host == "www.google.com"):
		b.interpreter++
		allowed = b.interpreter <= 1
	}
	b.mu.Unlock()
	if !allowed {
		return nil, errors.New("live validation wire budget or endpoint rejected")
	}
	return b.base.RoundTrip(r)
}

type budgetWire struct{ calls int }

func (w *budgetWire) RoundTrip(r *http.Request) (*http.Response, error) {
	w.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

func TestLiveBudgetOffline(t *testing.T) {
	wire := &budgetWire{}
	budget := &liveBudget{base: wire}
	for _, call := range []struct{ method, target string }{
		{"GET", "https://m.youtube.com/"},
		{"GET", "https://m.youtube.com/s/_/fixture.js"},
		{"POST", "https://jnn-pa.googleapis.com/$rpc/google.internal.waa.v1.Waa/GenerateIT"},
	} {
		req, _ := http.NewRequest(call.method, call.target, nil)
		resp, err := budget.RoundTrip(req)
		require.NoError(t, err)
		resp.Body.Close()
		{
			_, err := budget.RoundTrip(req)
			require.Error(t, err, "repeated initialization allowed")
		}
	}
	for _, call := range []struct{ method, target string }{
		{"POST", "https://www.youtube.com/youtubei/v1/player"},
		{"GET", "https://fixture.googlevideo.com/videoplayback"},
		{"GET", "http://www.google.com/botguard/fixture.js"},
		{"GET", "https://unrecognized.invalid/interpreter.js"},
	} {
		req, _ := http.NewRequest(call.method, call.target, nil)
		{
			_, err := budget.RoundTrip(req)
			require.Error(t, err, "unbudgeted endpoint allowed")
		}
	}
	assert.Equal(t, 3, wire.calls, "wire budget exceeded")
}

func TestLiveTokens(t *testing.T) {
	if os.Getenv("POTTER_LIVE") != "1" {
		t.Skip("set POTTER_LIVE=1 for a single controlled live initialization")
	}
	video := os.Getenv("POTTER_LIVE_VIDEO_ID")
	require.NotEmpty(t, video, "POTTER_LIVE_VIDEO_ID is required")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	budget := &liveBudget{base: transport}
	hc := &http.Client{Transport: budget, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("live redirects forbidden") }}
	c, err := potter.NewClient(potter.WithHTTPClient(hc))
	require.NoError(t, err)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	require.NoError(t, c.Warmup(ctx))
	proofs, err := c.Tokens(ctx, video)
	require.NoError(t, err)
	require.NotEmpty(t, proofs.Player, "missing Player proof")
	require.NotEmpty(t, proofs.GVS, "missing GVS proof")
	require.NotEmpty(t, proofs.VisitorData, "missing visitor identity")
	require.NotEmpty(t, proofs.ClientVersion, "missing client version")
	kind := "video"
	if proofs.GVSBinding != video {
		kind = "visitor"
		// Compare opaque identity values without printing them on failure.
		require.True(t, proofs.GVSBinding == proofs.VisitorData, "unexpected binding metadata")
	}
	again, err := c.Tokens(ctx, video)
	require.NoError(t, err, "warm cache check failed")
	// Preserve Go equality and avoid logging the live proofs in assertion diffs.
	require.True(t, again == proofs, "warm cache check failed")
	budget.mu.Lock()
	defer budget.mu.Unlock()
	t.Logf("Player bytes=%d GVS bytes=%d GVS binding=%s homepage=%d interpreter=%d GenerateIT=%d; remote Player/GVS acceptance untested", len(proofs.Player), len(proofs.GVS), kind, budget.homepage, budget.interpreter, budget.integrity)
}
