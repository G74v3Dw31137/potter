package potter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter/internal/botguard"
	"github.com/stretchr/testify/require"
)

// Reuse the protocol fixture and homepage configuration used throughout the
// offline suite. The transport never delegates to a real network transport.
func boundaryClient(t *testing.T, visitor string) *Client {
	t.Helper()
	script, err := os.ReadFile("internal/botguard/testdata/protocol-mock.js")
	require.NoError(t, err)
	page := `<script>ytcfg.set(` + strings.ReplaceAll(validConfig, "PRIVATE-visitor", visitor) + `);` + validChallenge + `</script>`
	c, err := NewClient(WithHTTPClient(&http.Client{Transport: homepageTransport(func(r *http.Request) (*http.Response, error) {
		raw := string(script)
		if r.URL.Path == "/" {
			raw = page
		}
		if r.Method == http.MethodPost {
			raw = `["VEVTVElU",3600,30]`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), Request: r}, nil
	})}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	return c
}

// Release is shared by the normal path and cleanup after a fatal assertion.
func boundaryBarrier() (<-chan struct{}, func()) {
	ch := make(chan struct{})
	var once sync.Once
	return ch, func() { once.Do(func() { close(ch) }) }
}

func TestPublicStaleHomepagePublication(t *testing.T) {
	for _, finish := range []string{"newer-generation", "close", "cancel"} {
		t.Run(finish, func(t *testing.T) {
			c := boundaryClient(t, "initial")
			require.NoError(t, c.Warmup(context.Background()))
			c.mu.Lock()
			first := c.session
			c.mu.Unlock()
			require.NotNil(t, first)
			c.mu.Lock()
			first.refreshAt = c.now() // demand starts a background replacement
			c.mu.Unlock()
			refreshEntered, staleEntered := make(chan struct{}), make(chan struct{})
			refreshFinish, releaseRefresh := boundaryBarrier()
			staleFinish, releaseStale := boundaryBarrier()
			var requests sync.WaitGroup
			// Registered after boundaryClient's Close cleanup: unblock both workers
			// and join the public request before Close joins the builder.
			t.Cleanup(func() {
				releaseRefresh()
				releaseStale()
				requests.Wait()
			})
			transport := c.http.Transport
			c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/" && r.Header.Get("X-Goog-Visitor-Id") != "" {
					// Hold the refresh body before revision allocation. The public fetch
					// gets N, then waits in parsing while this body gets N+1 and publishes.
					close(refreshEntered)
					<-refreshFinish
					response, err := transport.RoundTrip(r)
					if err != nil {
						return nil, err
					}
					raw, err := io.ReadAll(response.Body)
					response.Body.Close()
					if err != nil {
						return nil, err
					}
					response.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(string(raw), "initial", "newer")))
					return response, nil
				}
				return transport.RoundTrip(r)
			})
			c.parse = func(ctx context.Context, page []byte, revision uint64) (pageContext, error) {
				parsed, err := parsePage(ctx, page, revision)
				if strings.Contains(string(page), "initial") {
					close(staleEntered)
					<-staleFinish
				}
				return parsed, err
			}
			require.NoError(t, c.Warmup(context.Background()))
			<-refreshEntered
			c.mu.Lock()
			job := c.job
			// Model a separately observed homepage revision. It is incompatible
			// with the builder's source acquisition, so this test still exercises
			// stale publication rather than P2's compatible-build join.
			c.revision++
			c.current.guard.Revision = c.revision
			c.contextExpires = time.Time{}
			c.mu.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				tokens Tokens
				err    error
			}
			finished := make(chan result, 1)
			requests.Go(func() {
				v, e := c.Tokens(ctx, "video")
				finished <- result{v, e}
			})
			<-staleEntered
			if finish == "cancel" {
				cancel()
			}
			closed := make(chan struct{})
			if finish == "close" {
				go func() { _ = c.Close(); close(closed) }()
				<-c.life.Done()
			}
			releaseRefresh()
			<-job.done // newer generation (or actual Close) wins before the stale parse
			if finish == "newer-generation" {
				require.NoError(t, job.err)
				c.mu.Lock()
				current, published, generation := c.current.guard, c.session, c.generation
				var revision uint64
				if published != nil {
					revision = published.origin.Revision
				}
				c.mu.Unlock()
				require.NotNil(t, published)
				require.Equal(t, "newer", current.Visitor)
				require.Equal(t, current.Revision, revision)
				require.Equal(t, uint64(2), generation)
				require.NotSame(t, first, published)
			}
			releaseStale()
			got := <-finished
			switch finish {
			case "newer-generation":
				require.NoError(t, got.err)
				require.Equal(t, "newer", got.tokens.VisitorData)
				c.mu.Lock()
				current, published, generation := c.current.guard, c.session, c.generation
				var key string
				if published != nil {
					key = published.key
				}
				c.mu.Unlock()
				require.NotNil(t, published)
				require.Equal(t, "newer", current.Visitor)
				require.Equal(t, current.Key, key)
				require.Equal(t, uint64(2), generation, "stale publication must not trigger another build")
			case "cancel":
				require.ErrorIs(t, got.err, context.Canceled)
				require.NotErrorIs(t, got.err, ErrClosed)
				require.Equal(t, Tokens{}, got.tokens)
			case "close":
				<-closed
				require.ErrorIs(t, got.err, ErrClosed)
				require.Equal(t, Tokens{}, got.tokens)
				require.ErrorIs(t, c.Warmup(context.Background()), ErrClosed)
			}
		})
	}
}

func TestCoordinatorRejectsCanceledSuccessfulBuild(t *testing.T) {
	c := boundaryClient(t, "visitor")
	page, err := c.fetchContext(context.Background(), "")
	require.NoError(t, err)
	create := c.factory // real construction through the offline protocol transport
	entered := make(chan struct{})
	finish, release := boundaryBarrier()
	var built *session
	var buildErr error
	c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
		built, buildErr = create(ctx, input, bootstrap)
		close(entered)
		<-finish
		return built, buildErr // synchronous work completed despite cancellation
	}
	result := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		release()
		<-done
		// Also release a leaked unpublished handle if the assertion detects a bug.
		if built != nil {
			built.rt.Close()
		}
	})
	go func() {
		defer close(done)
		_, err := c.ensure(context.Background(), page.guard)
		result <- err
	}()
	<-entered
	require.NoError(t, buildErr)
	require.NotNil(t, built)
	// The builder is held before publication; establish the very same handle
	// can mint, then cancel it. A missing coordinator Close now leaves it usable.
	proof, err := built.rt.Mint(context.Background(), "video")
	require.NoError(t, err)
	require.NotEmpty(t, proof)
	c.mu.Lock()
	job := c.job
	c.mu.Unlock()
	require.NotNil(t, job)
	job.cancel()
	release()
	require.ErrorIs(t, <-result, context.Canceled)
	c.mu.Lock()
	published, current, generation, pending := c.session, c.current.guard, c.generation, c.job
	c.mu.Unlock()
	require.Nil(t, published)
	require.Empty(t, current.Key)
	require.Zero(t, generation)
	require.Nil(t, pending)
	proof, err = built.rt.Mint(context.Background(), "video")
	require.Empty(t, proof)
	require.ErrorIs(t, err, ErrClosed)
}

func TestPublicClientsIndependentDuringExecution(t *testing.T) {
	for _, action := range []string{"cancel", "close"} {
		t.Run(action, func(t *testing.T) {
			a, b := boundaryClient(t, "identity-a"), boundaryClient(t, "identity-b")
			entered := make(chan struct{})
			var once sync.Once
			a.engine.MintStarted = func() { once.Do(func() { close(entered) }) }
			var wg sync.WaitGroup
			var warmA, warmB error
			wg.Go(func() { warmA = a.Warmup(context.Background()) })
			wg.Go(func() { warmB = b.Warmup(context.Background()) })
			wg.Wait()
			require.NoError(t, warmA)
			require.NoError(t, warmB)
			first, err := a.Tokens(context.Background(), "video")
			require.NoError(t, err)
			cached, err := b.Tokens(context.Background(), "video")
			require.NoError(t, err)
			require.NotEqual(t, first.VisitorData, cached.VisitorData)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				tokens Tokens
				err    error
			}
			finished := make(chan result, 1)
			done := make(chan struct{})
			t.Cleanup(func() { cancel(); <-done })
			go func() {
				defer close(done)
				v, e := a.Tokens(ctx, "hang")
				finished <- result{v, e}
			}()
			<-entered // fixture calls the Go barrier immediately before while(true){}
			// B can still mint and reuse its cache while A's JS loop is active.
			same, err := b.Tokens(context.Background(), "video")
			require.NoError(t, err)
			require.Equal(t, cached, same)
			next, err := b.Tokens(context.Background(), "next")
			require.NoError(t, err)
			require.Equal(t, "identity-b", next.VisitorData)
			require.Equal(t, int64(1), b.stats.init.Load())
			if action == "cancel" {
				cancel()
			} else {
				require.NoError(t, a.Close())
			}
			got := <-finished
			require.ErrorIs(t, got.err, context.Canceled)
			require.ErrorIs(t, got.err, ErrMint)
			require.NotErrorIs(t, got.err, context.DeadlineExceeded)
			require.Equal(t, Tokens{}, got.tokens)
			if action == "cancel" {
				recovered, err := a.Tokens(context.Background(), "video")
				require.NoError(t, err)
				require.Equal(t, "identity-a", recovered.VisitorData)
				require.Equal(t, int64(2), a.stats.init.Load())
			} else {
				require.Nil(t, a.session)
				require.Nil(t, a.job)
				require.ErrorIs(t, a.Warmup(context.Background()), ErrClosed)
			}
			// Closing A, including after rebuilding it, leaves B's runtime and cache live.
			require.NoError(t, a.Close())
			same, err = b.Tokens(context.Background(), "video")
			require.NoError(t, err)
			require.Equal(t, cached, same)
			require.Equal(t, int64(1), b.stats.init.Load())
		})
	}
}

func TestFailedGenerationDropsProofCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := boundaryClient(t, "visitor")
		// The normal first request proves the cache is populated before a later failure.
		_, err := c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		old := c.session
		require.NotEmpty(t, old.tokens)
		got, err := c.Tokens(context.Background(), "reject")
		require.Equal(t, Tokens{}, got)
		require.ErrorIs(t, err, ErrMint)
		require.True(t, old.dead.Load())
		require.True(t, old.retired)
		require.Nil(t, old.tokens)
		_, err = old.rt.Mint(context.Background(), "video")
		require.ErrorIs(t, err, ErrClosed)
		// Backoff retains the classified failure, never the earlier cached proofs.
		got, err = c.Tokens(context.Background(), "video")
		require.Equal(t, Tokens{}, got)
		require.ErrorIs(t, err, ErrMint)
		time.Sleep(5 * time.Second) // advance policy time in the virtual clock
		_, err = c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		require.NotSame(t, old, c.session)
		require.Greater(t, c.session.generation, old.generation)
	})
}

func TestPublicationOutcomeClassification(t *testing.T) {
	c, input := coordinatorFixture(t)
	stale := input
	stale.Revision--
	invalid := input
	invalid.Config = []byte("PRIVATE invalid")
	c.mu.Lock()
	publishedErr := c.publishContextLocked(input)
	staleErr := c.publishContextLocked(stale)
	invalidErr := c.publishContextLocked(invalid)
	c.mu.Unlock()
	require.NoError(t, publishedErr)
	require.ErrorIs(t, staleErr, ErrIdentity)
	require.ErrorIs(t, invalidErr, ErrChallenge)
	require.NotErrorIs(t, invalidErr, ErrClosed)
	require.NotContains(t, fmt.Sprint(invalidErr), "PRIVATE")
	require.NoError(t, c.Close())
	c.mu.Lock()
	closedErr := c.publishContextLocked(input)
	c.mu.Unlock()
	require.ErrorIs(t, closedErr, ErrClosed)
}
