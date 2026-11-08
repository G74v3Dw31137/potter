package potter

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/G74v3Dw31137/potter/internal/botguard"
	"github.com/stretchr/testify/require"
)

func readSnapshot(t *testing.T, s SessionSnapshot) (string, []byte) {
	t.Helper()
	u, r := s.Homepage()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return u, b
}

// Rewrite fixture homepage responses without changing the actual HTTP/JS path.
func rewriteSnapshotPage(c *Client, transform func(string) string, documentURL string) {
	base := c.http.Transport
	c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
		resp, err := base.RoundTrip(r)
		if err != nil || r.URL.Path != "/" {
			return resp, err
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(strings.NewReader(transform(string(b))))
		if documentURL != "" {
			u, _ := url.Parse(documentURL)
			resp.Request = &http.Request{URL: u}
		}
		return resp, nil
	})
}

func TestSnapshotSameIdentityDifferentHomepage(t *testing.T) {
	c := boundaryClient(t, "visitor")
	first, err := c.TokensWithSession(context.Background(), "video")
	require.NoError(t, err)
	u, original := readSnapshot(t, first)
	c.mu.Lock()
	origin := c.session.origin
	c.contextExpires = time.Time{}
	c.mu.Unlock()
	// Request context and player reference are outside the normalized client key.
	rewriteSnapshotPage(c, func(page string) string {
		return strings.Replace(page, `"INNERTUBE_CONTEXT":{`, `"INNERTUBE_CONTEXT":{"request":{"marker":"new acquisition"},`, 1) + `<script>var playerRef="/s/player/new/base.js";</script>`
	}, "https://www.youtube.com/?new")
	got, err := c.TokensWithSession(context.Background(), "video")
	require.NoError(t, err)
	c.mu.Lock()
	current := c.current.guard
	minterOrigin := c.session.origin
	c.mu.Unlock()
	require.Equal(t, origin.Key, current.Key)
	require.Greater(t, current.Revision, origin.Revision)
	require.NotEqual(t, original, current.Page)
	require.NotEqual(t, origin.DocumentURL, current.DocumentURL)
	require.Equal(t, origin.Revision, minterOrigin.Revision)
	u2, body := readSnapshot(t, got)
	require.Equal(t, u, u2)
	require.Equal(t, original, body)
	require.Equal(t, first.Tokens(), got.Tokens())
	require.Equal(t, first.Generation(), got.Generation())
	// The artifact shares the original acquired storage, rather than copying the
	// whole page for each acquisition. Accessors do not initialize or mint.
	require.True(t, &origin.Page[0] == &got.page[0], "snapshot must share original page storage")
	for range 5 {
		_, _ = got.Homepage()
		_ = got.Tokens()
		_ = got.Generation()
	}
	require.Equal(t, int64(1), c.stats.init.Load())
	require.Equal(t, int64(2), c.stats.mints.Load())
	require.Equal(t, int64(1), c.engine.Stats().GenerateITRequests)
}

func TestSnapshotUnknownClientFieldReplacesGeneration(t *testing.T) {
	c := boundaryClient(t, "visitor")
	first, err := c.TokensWithSession(context.Background(), "video")
	require.NoError(t, err)
	rewriteSnapshotPage(c, func(page string) string { return strings.Replace(page, `"client":{`, `"client":{"unknown":"new",`, 1) }, "")
	c.mu.Lock()
	c.contextExpires = time.Time{}
	c.mu.Unlock()
	next, err := c.TokensWithSession(context.Background(), "video")
	require.NoError(t, err)
	require.Equal(t, uint64(2), next.Generation())
	require.Equal(t, first.Tokens().VisitorData, next.Tokens().VisitorData)
	require.Equal(t, first.Tokens().ClientVersion, next.Tokens().ClientVersion)
	_, a := readSnapshot(t, first)
	_, b := readSnapshot(t, next)
	require.NotEqual(t, a, b)
}

func TestSnapshotReplacementDuringLease(t *testing.T) {
	for _, cacheHit := range []bool{false, true} {
		for _, changedKey := range []bool{false, true} {
			name := "mint"
			if cacheHit {
				name = "cache"
			}
			if changedKey {
				name += "-changed-key"
			}
			t.Run(name, func(t *testing.T) {
				c := boundaryClient(t, "visitor")
				// Turn the fixture's infinite loop into a synchronous barrier that
				// resumes normal minting after the replacement has been published.
				base := c.http.Transport
				c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
					resp, err := base.RoundTrip(r)
					if err != nil || r.URL.Path == "/" || r.Method != "GET" {
						return resp, err
					}
					b, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						return nil, err
					}
					resp.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(string(b), "while(true){}", "")))
					return resp, nil
				})
				leaseEntered := make(chan struct{})
				leaseFinish, releaseLease := boundaryBarrier()
				var enteredOnce sync.Once
				c.engine.MintStarted = func() { enteredOnce.Do(func() { close(leaseEntered) }); <-leaseFinish }
				first, err := c.TokensWithSession(context.Background(), "video")
				require.NoError(t, err)
				u, original := readSnapshot(t, first)
				buildEntered := make(chan struct{})
				buildFinish, releaseBuild := boundaryBarrier()
				create := c.factory
				c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
					close(buildEntered)
					<-buildFinish
					return create(ctx, input, bootstrap)
				}
				rewriteSnapshotPage(c, func(page string) string {
					if changedKey {
						page = strings.ReplaceAll(page, `"visitor"`, `"replacement"`)
					}
					return page + "<!--generation two-->"
				}, "https://www.youtube.com/?generation=two")
				c.mu.Lock()
				old := c.session
				old.refreshAt = c.now()
				c.mu.Unlock()
				require.NoError(t, c.Warmup(context.Background()))
				<-buildEntered
				c.mu.Lock()
				job := c.job
				c.mu.Unlock()
				if cacheHit {
					// With the builder held, the fourth now call is the cached GVS
					// lookup, after ensure, the session deadline and Player lookup.
					var ticks atomic.Int64
					c.now = func() time.Time {
						if ticks.Add(1) == 4 {
							close(leaseEntered)
							<-leaseFinish
						}
						return time.Now()
					}
				}
				type result struct {
					snapshot SessionSnapshot
					err      error
				}
				done := make(chan result, 1)
				t.Cleanup(func() { releaseBuild(); releaseLease() })
				go func() {
					video := "hang"
					if cacheHit {
						video = "video"
					}
					s, err := c.TokensWithSession(context.Background(), video)
					done <- result{s, err}
				}()
				<-leaseEntered
				releaseBuild()
				<-job.done
				require.NoError(t, job.err)
				c.mu.Lock()
				generation := c.generation
				oldUsers := old.users
				published := c.session
				c.mu.Unlock()
				require.Equal(t, uint64(2), generation)
				require.Equal(t, 1, oldUsers)
				releaseLease()
				got := <-done
				if changedKey {
					// Existing identity retry adopts the newly published complete session.
					require.NoError(t, got.err)
					require.Equal(t, uint64(2), got.snapshot.Generation())
					require.Equal(t, "replacement", got.snapshot.Tokens().VisitorData)
					u2, b := readSnapshot(t, got.snapshot)
					require.Equal(t, published.origin.DocumentURL, u2)
					require.Equal(t, published.origin.Page, b)
				} else {
					require.NoError(t, got.err)
					require.Equal(t, first.Generation(), got.snapshot.Generation())
					u2, b := readSnapshot(t, got.snapshot)
					require.Equal(t, u, u2)
					require.Equal(t, original, b)
					if cacheHit {
						require.Equal(t, first.Tokens(), got.snapshot.Tokens())
					} else {
						require.NotEmpty(t, got.snapshot.Tokens().Player)
						require.NotEmpty(t, got.snapshot.Tokens().GVS)
					}
				}
				require.NoError(t, c.Close())
				_, b := readSnapshot(t, first)
				require.Equal(t, original, b)
			})
		}
	}
}

func TestSnapshotRuntimeAndMintBudget(t *testing.T) {
	for _, snapshotFirst := range []bool{false, true} {
		c := boundaryClient(t, "visitor")
		if snapshotFirst {
			_, err := c.TokensWithSession(context.Background(), "video")
			require.NoError(t, err)
		} else {
			_, err := c.Tokens(context.Background(), "video")
			require.NoError(t, err)
		}
		require.Equal(t, int64(1), c.stats.init.Load())
		require.Equal(t, int64(2), c.stats.mints.Load())
		before := c.engine.Stats()
		for range 10 {
			s, err := c.TokensWithSession(context.Background(), "video")
			require.NoError(t, err)
			_, _ = s.Homepage()
			_ = s.Tokens()
			_ = s.Generation()
		}
		require.Equal(t, before, c.engine.Stats())
		require.Equal(t, int64(1), c.stats.init.Load())
		require.Equal(t, int64(2), c.stats.mints.Load())
		require.Equal(t, int64(20), c.stats.hits.Load())
	}
}

func TestSnapshotDocumentURLProvenance(t *testing.T) {
	for _, metadata := range []string{"normal", "nil-request", "nil-url", "empty-url", "relative", "invalid-origin", "http", "port"} {
		for _, redirect := range []bool{false, true} {
			name := metadata
			if redirect {
				name += "-redirect"
			}
			t.Run(name, func(t *testing.T) {
				c := boundaryClient(t, "visitor")
				base := c.http.Transport
				want := homepage
				if redirect {
					want = "https://www.youtube.com/?document=final"
				}
				var homeCalls atomic.Int64
				c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path != "/" {
						return base.RoundTrip(r)
					}
					homeCalls.Add(1)
					if redirect && r.URL.Host == "m.youtube.com" {
						return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{want}}, Body: http.NoBody}, nil
					}
					resp, err := base.RoundTrip(r)
					if err != nil {
						return nil, err
					}
					switch metadata {
					case "nil-request":
						resp.Request = nil
					case "nil-url":
						resp.Request = &http.Request{}
					case "empty-url":
						resp.Request = &http.Request{URL: &url.URL{}}
					case "relative", "invalid-origin", "http", "port":
						value := map[string]string{"relative": "/?partial", "invalid-origin": "https://evil.invalid/", "http": "http://m.youtube.com/", "port": "https://m.youtube.com:8443/"}[metadata]
						u, _ := url.Parse(value)
						resp.Request = &http.Request{URL: u}
					}
					return resp, nil
				})
				s, err := c.TokensWithSession(context.Background(), "video")
				if metadata == "relative" || metadata == "invalid-origin" || metadata == "http" || metadata == "port" {
					requireHomepageCode(t, err, "invalid-document-url")
					require.Equal(t, uint64(0), s.Generation())
					require.Equal(t, Tokens{}, s.Tokens())
					u, b := readSnapshot(t, s)
					require.Empty(t, u)
					require.Empty(t, b)
				} else {
					require.NoError(t, err)
					u, b := readSnapshot(t, s)
					require.Equal(t, want, u)
					c.mu.Lock()
					origin := c.session.origin
					c.mu.Unlock()
					require.Equal(t, origin.Page, b)
					require.Equal(t, origin.DocumentURL, u)
				}
				count := int64(1)
				if redirect {
					count = 2
				}
				require.Equal(t, count, homeCalls.Load())
			})
		}
	}
}

func TestSnapshotRejectedHomepageRedirects(t *testing.T) {
	for _, target := range []string{"https://unapproved.invalid/?PRIVATE", "http://m.youtube.com/", "https://m.youtube.com:8443/", "https://PRIVATE@m.youtube.com/", "https://www.youtube.com/watch"} {
		t.Run(target, func(t *testing.T) {
			c := boundaryClient(t, "visitor")
			var calls atomic.Int64
			c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: http.NoBody, Request: r}, nil
			})
			s, err := c.TokensWithSession(context.Background(), "video")
			require.ErrorIs(t, err, ErrChallenge)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.Equal(t, Tokens{}, s.Tokens())
			require.Zero(t, s.Generation())
			u, b := readSnapshot(t, s)
			require.Empty(t, u)
			require.Empty(t, b)
			require.Equal(t, int64(1), calls.Load(), "disallowed redirect must not reach transport")
		})
	}
}
