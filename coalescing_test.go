package potter

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter/internal/botguard"
	"github.com/stretchr/testify/require"
)

// Counts only the acquisition under test, after the initial generation. Every
// request uses the offline protocol fixture. Extra requests hit the same barrier
// so quiescence exposes a budget violation without relying on scheduling.
type coalescingHTTP struct {
	calls   atomic.Int64
	status  atomic.Int64
	entered chan struct{}
	finish  <-chan struct{}
	release func()
}

func coalescingClient(t *testing.T, replace bool) (*Client, SessionSnapshot, *coalescingHTTP) {
	t.Helper()
	c := boundaryClient(t, "visitor")
	first, err := c.TokensWithSession(context.Background(), "video")
	require.NoError(t, err)
	rewriteSnapshotPage(c, func(page string) string {
		if replace {
			page = strings.ReplaceAll(page, `"visitor"`, `"replacement"`)
			page = strings.ReplaceAll(page, "fixture-version", "replacement-version")
		}
		return page + "<!--shared acquisition-->"
	}, "https://www.youtube.com/?acquisition=shared")
	finish, release := boundaryBarrier()
	wire := &coalescingHTTP{entered: make(chan struct{}), finish: finish, release: release}
	base := c.http.Transport
	var once sync.Once
	c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/" {
			wire.calls.Add(1)
			once.Do(func() { close(wire.entered) })
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-wire.finish:
			}
			if status := wire.status.Load(); status != 0 {
				return &http.Response{StatusCode: int(status), Header: make(http.Header), Body: http.NoBody, Request: r}, nil
			}
		}
		return base.RoundTrip(r)
	})
	t.Cleanup(release)
	return c, first, wire
}

func startCoalescingRefresh(t *testing.T, c *Client, wire *coalescingHTTP) *build {
	t.Helper()
	c.mu.Lock()
	c.session.refreshAt = c.now()
	c.mu.Unlock()
	require.NoError(t, c.Warmup(context.Background()))
	<-wire.entered
	c.mu.Lock()
	defer c.mu.Unlock()
	c.contextExpires = time.Time{}
	require.NotNil(t, c.job)
	return c.job
}

type coalescingResult struct {
	snapshot SessionSnapshot
	err      error
}

func coalescingTokens(c *Client, ctx context.Context) <-chan coalescingResult {
	done := make(chan coalescingResult, 1)
	go func() {
		s, err := c.TokensWithSession(ctx, "video")
		done <- coalescingResult{s, err}
	}()
	return done
}

func requireSharedSnapshot(t *testing.T, c *Client, first, got SessionSnapshot) {
	t.Helper()
	require.Equal(t, first.Generation()+1, got.Generation())
	u, body := readSnapshot(t, got)
	require.Equal(t, "https://www.youtube.com/?acquisition=shared", u)
	require.Contains(t, string(body), "<!--shared acquisition-->")
	c.mu.Lock()
	defer c.mu.Unlock()
	require.Equal(t, c.generation, got.Generation())
	require.Equal(t, c.current.guard.Revision, c.session.origin.Revision)
	require.Equal(t, c.current.guard.Key, c.session.key)
	require.Equal(t, c.session.origin.Page, body)
	require.Equal(t, c.session.origin.DocumentURL, u)
	require.Equal(t, c.session.origin.Visitor, got.Tokens().VisitorData)
	require.Equal(t, c.session.clientVersion, got.Tokens().ClientVersion)
}

func TestHomepageCoalescingForegroundBackground(t *testing.T) {
	for _, replace := range []bool{false, true} {
		name := "same-identity"
		if replace {
			name = "replacement-identity"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, first, wire := coalescingClient(t, replace)
				job := startCoalescingRefresh(t, c, wire)
				var results []<-chan coalescingResult
				for range 12 {
					results = append(results, coalescingTokens(c, context.Background()))
				}
				synctest.Wait() // foreground holds gate waiting for the background build
				require.Equal(t, int64(1), wire.calls.Load())
				wire.release()
				<-job.done
				require.NoError(t, job.err)
				for _, result := range results {
					got := <-result
					require.NoError(t, got.err)
					requireSharedSnapshot(t, c, first, got.snapshot)
					if replace {
						require.Equal(t, "replacement", got.snapshot.Tokens().VisitorData)
						require.Equal(t, "replacement-version", got.snapshot.Tokens().ClientVersion)
					}
				}
				require.Equal(t, int64(1), wire.calls.Load(), "one homepage for the entire overlap")
				require.Equal(t, int64(2), c.stats.init.Load())
				require.Equal(t, int64(2), c.engine.Stats().GenerateITRequests)
				require.NoError(t, c.Close())
				_, oldPage := readSnapshot(t, first)
				require.NotContains(t, string(oldPage), "<!--shared acquisition-->")
			})
		})
	}
}

func TestHomepageCoalescingConcurrentForeground(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := boundaryClient(t, "visitor")
		base := c.http.Transport
		entered := make(chan struct{})
		finish, release := boundaryBarrier()
		t.Cleanup(release)
		var calls atomic.Int64
		var once sync.Once
		c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/" {
				calls.Add(1)
				once.Do(func() { close(entered) })
				select {
				case <-finish:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}
			return base.RoundTrip(r)
		})
		var results []<-chan coalescingResult
		for range 12 {
			results = append(results, coalescingTokens(c, context.Background()))
		}
		<-entered
		synctest.Wait()
		require.Equal(t, int64(1), calls.Load())
		release()
		for _, result := range results {
			got := <-result
			require.NoError(t, got.err)
			require.Equal(t, uint64(1), got.snapshot.Generation())
			u, page := readSnapshot(t, got.snapshot)
			require.Equal(t, homepage, u)
			require.NotEmpty(t, page)
		}
		require.Equal(t, int64(1), calls.Load())
		require.Equal(t, int64(1), c.stats.init.Load())
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingReplacesCachedTokensAfterWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, false)
		// Expire only the token cache. The live old minter now produces a
		// distinguishable pair for the same identity and video binding.
		time.Sleep(2 * time.Minute)
		previous, err := c.TokensWithSession(context.Background(), "video")
		require.NoError(t, err)
		require.Equal(t, first.Generation(), previous.Generation())
		require.NotEqual(t, first.Tokens().Player, previous.Tokens().Player)
		require.NotEqual(t, first.Tokens().GVS, previous.Tokens().GVS)
		job := startCoalescingRefresh(t, c, wire)
		waiter := coalescingTokens(c, context.Background())
		synctest.Wait()
		select {
		case <-waiter:
			t.Fatal("foreground returned before the compatible builder completed")
		default:
		}
		wire.release()
		got := <-waiter
		require.NoError(t, got.err)
		<-job.done
		require.NoError(t, job.err)
		requireSharedSnapshot(t, c, previous, got.snapshot)
		require.NotEqual(t, previous.Tokens().Player, got.snapshot.Tokens().Player)
		require.NotEqual(t, previous.Tokens().GVS, got.snapshot.Tokens().GVS)
		require.Equal(t, first.Tokens(), got.snapshot.Tokens(), "replacement minter starts its own counter")
		require.Equal(t, int64(1), wire.calls.Load())
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingFailureAndRetry(t *testing.T) {
	for _, stage := range []string{"homepage", "parse", "integrity"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, first, wire := coalescingClient(t, false)
				var failIntegrity atomic.Bool
				if stage == "homepage" {
					wire.status.Store(http.StatusForbidden)
				} else if stage == "parse" {
					wire.status.Store(http.StatusOK) // empty, invalid homepage
				} else {
					failIntegrity.Store(true)
					base := c.http.Transport
					c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
						if r.Method == http.MethodPost && failIntegrity.Load() {
							return &http.Response{StatusCode: 403, Header: make(http.Header), Body: http.NoBody, Request: r}, nil
						}
						return base.RoundTrip(r)
					})
				}
				job := startCoalescingRefresh(t, c, wire)
				result := coalescingTokens(c, context.Background())
				synctest.Wait()
				require.Equal(t, int64(1), wire.calls.Load())
				wire.release()
				got := <-result
				want := ErrChallenge
				if stage == "integrity" {
					want = ErrGenerateIT
				}
				require.ErrorIs(t, got.err, want)
				require.Equal(t, SessionSnapshot{}, got.snapshot)
				<-job.done
				require.ErrorIs(t, job.err, want)
				require.Equal(t, int64(1), wire.calls.Load(), "failed sharing must not trigger a fallback fetch")
				c.mu.Lock()
				require.Equal(t, first.Generation(), c.generation)
				require.Equal(t, first.documentURL, c.current.guard.DocumentURL)
				require.Equal(t, first.page, c.current.guard.Page)
				require.Nil(t, c.job, "completion must clear the shared acquisition")
				retryAt := c.retryAt
				c.mu.Unlock()
				require.True(t, c.now().Before(retryAt))
				wire.status.Store(0)
				failIntegrity.Store(false)
				// Virtual time reaches the actual policy deadline, including jitter;
				// the test does not bypass or rewrite the coordinator's backoff.
				time.Sleep(retryAt.Sub(c.now()))
				// A failed acquisition does not leave a completed flight to be reused.
				require.NoError(t, c.Warmup(context.Background()))
				synctest.Wait()
				got.snapshot, got.err = c.TokensWithSession(context.Background(), "video")
				require.NoError(t, got.err)
				requireSharedSnapshot(t, c, first, got.snapshot)
				require.Equal(t, int64(2), wire.calls.Load(), "one failed acquisition and one retry")
				require.NoError(t, c.Close())
			})
		})
	}
}

func TestHomepageCoalescingWaiterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, false)
		job := startCoalescingRefresh(t, c, wire)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		waiter := coalescingTokens(c, ctx)
		synctest.Wait()
		queuedCtx, cancelQueued := context.WithCancel(context.Background())
		defer cancelQueued()
		queued := coalescingTokens(c, queuedCtx)
		synctest.Wait()
		cancelQueued()
		require.ErrorIs(t, (<-queued).err, context.Canceled)
		cancel()
		got := <-waiter // returns while transport is still held
		require.ErrorIs(t, got.err, context.Canceled)
		require.Equal(t, SessionSnapshot{}, got.snapshot)
		require.NoError(t, job.work.Err(), "a waiter does not own the shared build")
		healthy := coalescingTokens(c, context.Background())
		synctest.Wait()
		require.Equal(t, int64(1), wire.calls.Load())
		wire.release()
		got = <-healthy
		require.NoError(t, got.err)
		requireSharedSnapshot(t, c, first, got.snapshot)
		require.Equal(t, int64(1), wire.calls.Load())
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingInitiatorCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, true)
		// Force the initiating foreground caller to wait for a new builder,
		// rather than returning immediately with a usable old session.
		c.mu.Lock()
		c.session.dead.Store(true)
		c.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		initiator := make(chan error, 1)
		go func() { initiator <- c.Warmup(ctx) }()
		<-wire.entered
		c.mu.Lock()
		job := c.job
		c.contextExpires = time.Time{}
		c.mu.Unlock()
		healthy := coalescingTokens(c, context.Background())
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-initiator, context.Canceled)
		synctest.Wait() // queued foreground now joins the initiating caller's builder
		require.NoError(t, job.work.Err())
		require.Equal(t, int64(1), wire.calls.Load())
		wire.release()
		got := <-healthy
		require.NoError(t, got.err)
		requireSharedSnapshot(t, c, first, got.snapshot)
		require.Equal(t, int64(1), wire.calls.Load())
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingBuildCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, false)
		job := startCoalescingRefresh(t, c, wire)
		waiter := coalescingTokens(c, context.Background())
		synctest.Wait()
		job.cancel() // the acquisition's own budget/lifecycle, not a waiter
		got := <-waiter
		require.ErrorIs(t, got.err, context.Canceled)
		require.Equal(t, SessionSnapshot{}, got.snapshot)
		<-job.done
		require.Equal(t, int64(1), wire.calls.Load())
		c.mu.Lock()
		require.Equal(t, first.Generation(), c.generation)
		require.Equal(t, first.page, c.current.guard.Page)
		c.retryAt = time.Time{}
		c.mu.Unlock()
		wire.release()
		require.NoError(t, c.Warmup(context.Background()))
		synctest.Wait()
		got.snapshot, got.err = c.TokensWithSession(context.Background(), "video")
		require.NoError(t, got.err)
		requireSharedSnapshot(t, c, first, got.snapshot)
		require.Equal(t, int64(2), wire.calls.Load())
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _, wire := coalescingClient(t, false)
		job := startCoalescingRefresh(t, c, wire)
		waiter := coalescingTokens(c, context.Background())
		synctest.Wait()
		var queued []<-chan coalescingResult
		for range 8 {
			queued = append(queued, coalescingTokens(c, context.Background()))
		}
		synctest.Wait()
		require.NoError(t, c.Close()) // transport unblocks through its request context
		got := <-waiter
		require.ErrorIs(t, got.err, ErrClosed)
		require.Equal(t, SessionSnapshot{}, got.snapshot)
		for _, result := range queued {
			got := <-result
			require.Error(t, got.err)
			require.Equal(t, SessionSnapshot{}, got.snapshot)
		}
		<-job.done
		require.ErrorIs(t, job.err, ErrClosed)
		require.Equal(t, int64(1), wire.calls.Load())
		c.mu.Lock()
		require.Nil(t, c.job)
		require.Nil(t, c.session)
		require.Empty(t, c.current.guard.Key)
		c.mu.Unlock()
	})
}

func TestHomepageCoalescingThroughPublication(t *testing.T) {
	for _, outcome := range []string{"success", "cancel-waiter", "close"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, first, wire := coalescingClient(t, true)
				entered := make(chan struct{})
				finish, release := boundaryBarrier()
				t.Cleanup(release)
				create := c.factory
				c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
					s, err := create(ctx, input, bootstrap)
					close(entered)
					<-finish // completed synchronous work can delay cancellation
					return s, err
				}
				job := startCoalescingRefresh(t, c, wire)
				wire.release()
				<-entered // homepage and integrity work have already completed
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				waiter := coalescingTokens(c, ctx)
				synctest.Wait()
				require.Equal(t, int64(1), wire.calls.Load(), "sharing lasts until validated publication")
				c.mu.Lock()
				require.Equal(t, first.Generation(), c.generation)
				require.Equal(t, first.page, c.current.guard.Page, "an unfinished session cannot publish its homepage")
				c.mu.Unlock()
				var closing chan error
				switch outcome {
				case "cancel-waiter":
					cancel()
					got := <-waiter
					require.ErrorIs(t, got.err, context.Canceled)
					waiter = coalescingTokens(c, context.Background())
					synctest.Wait()
				case "close":
					closing = make(chan error, 1)
					go func() { closing <- c.Close() }()
					<-c.life.Done()
					got := <-waiter // waiter is not stranded behind synchronous work
					require.ErrorIs(t, got.err, ErrClosed)
					require.Equal(t, SessionSnapshot{}, got.snapshot)
				}
				release()
				<-job.done
				if outcome == "close" {
					require.NoError(t, <-closing)
					require.ErrorIs(t, job.err, ErrClosed)
					require.Equal(t, first.Generation(), c.generation)
				} else {
					got := <-waiter
					require.NoError(t, got.err)
					requireSharedSnapshot(t, c, first, got.snapshot)
				}
				require.Equal(t, int64(1), wire.calls.Load())
				require.NoError(t, c.Close())
			})
		})
	}
}

func TestHomepageCoalescingReplacementDuringWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, false)
		entered := make(chan struct{})
		finish, release := boundaryBarrier()
		t.Cleanup(release)
		create := c.factory
		var attempts atomic.Int64
		var superseded *session
		c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
			s, err := create(ctx, input, bootstrap)
			if attempts.Add(1) == 1 {
				superseded = s
				close(entered)
				<-finish
			}
			return s, err
		}
		job := startCoalescingRefresh(t, c, wire)
		waiter := coalescingTokens(c, context.Background())
		synctest.Wait()
		wire.release()
		<-entered
		require.NotNil(t, superseded)
		// Model a separately acquired context arriving after the join decision.
		// Its revision is newer than both the initial page and the held build.
		c.mu.Lock()
		c.revision++
		revision := c.revision
		c.mu.Unlock()
		page := strings.ReplaceAll(string(first.page), `"visitor"`, `"replacement"`) + "<!--replacement won-->"
		next, err := parsePage(context.Background(), []byte(page), revision)
		require.NoError(t, err)
		next.guard.DocumentURL = "https://www.youtube.com/?replacement=won"
		c.mu.Lock()
		publishErr := c.publishContextLocked(next.guard)
		c.observeLocked(next.guard)
		c.mu.Unlock()
		require.NoError(t, publishErr)
		release()
		<-job.done
		require.ErrorIs(t, job.err, ErrIdentity)
		got := <-waiter
		require.NoError(t, got.err)
		require.Equal(t, first.Generation()+1, got.snapshot.Generation(), "superseded build must not consume a generation")
		require.Equal(t, "replacement", got.snapshot.Tokens().VisitorData)
		u, body := readSnapshot(t, got.snapshot)
		require.Equal(t, next.guard.DocumentURL, u)
		require.Equal(t, next.guard.Page, body)
		c.mu.Lock()
		require.Equal(t, revision, c.session.origin.Revision)
		require.Equal(t, revision, c.current.guard.Revision)
		c.mu.Unlock()
		_, err = superseded.rt.Mint(context.Background(), "video")
		require.ErrorIs(t, err, ErrClosed, "unpublished superseded runtime must be released")
		require.Equal(t, int64(1), wire.calls.Load())
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingDifferentSourceRevision(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, false)
		job := startCoalescingRefresh(t, c, wire)
		c.mu.Lock()
		// Same normalized identity, but a separately observed homepage/config
		// revision is deliberately outside the builder's acquisition opportunity.
		c.revision++
		c.current.guard.Revision = c.revision
		c.mu.Unlock()
		waiter := coalescingTokens(c, context.Background())
		synctest.Wait()
		require.Equal(t, int64(2), wire.calls.Load(), "different source revisions must not share")
		wire.release()
		require.NoError(t, (<-waiter).err)
		<-job.done
		synctest.Wait()
		got, err := c.TokensWithSession(context.Background(), "video")
		require.NoError(t, err)
		require.GreaterOrEqual(t, got.Generation(), first.Generation())
		u, body := readSnapshot(t, got)
		c.mu.Lock()
		require.Equal(t, c.session.generation, got.Generation())
		require.Equal(t, c.session.origin.DocumentURL, u)
		require.Equal(t, c.session.origin.Page, body)
		require.GreaterOrEqual(t, c.current.guard.Revision, c.session.origin.Revision)
		c.mu.Unlock()
		require.Equal(t, int64(2), wire.calls.Load())
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingRedirect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, false)
		base := c.http.Transport
		var initial, final atomic.Int64
		c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/" {
				if r.URL.Host == "m.youtube.com" {
					initial.Add(1)
					return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://www.youtube.com/?acquisition=shared"}}, Body: http.NoBody, Request: r}, nil
				}
				final.Add(1)
				if r.Header.Get("X-Goog-Visitor-Id") != "" {
					return nil, ErrIdentity // redirect must strip the session header
				}
			}
			return base.RoundTrip(r)
		})
		job := startCoalescingRefresh(t, c, wire)
		waiter := coalescingTokens(c, context.Background())
		synctest.Wait()
		require.Equal(t, int64(1), initial.Load())
		require.Equal(t, int64(1), final.Load())
		wire.release()
		got := <-waiter
		require.NoError(t, got.err)
		<-job.done
		require.NoError(t, job.err)
		requireSharedSnapshot(t, c, first, got.snapshot)
		require.Equal(t, int64(1), initial.Load(), "one homepage acquisition")
		require.Equal(t, int64(1), final.Load(), "one permitted redirect hop")
		require.NoError(t, c.Close())
	})
}

func TestHomepageCoalescingBeforeDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, first, wire := coalescingClient(t, false)
		entered := make(chan struct{})
		finish, release := boundaryBarrier()
		t.Cleanup(release)
		create := c.factory
		c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
			close(entered)
			<-finish
			return create(ctx, input, bootstrap)
		}
		c.mu.Lock()
		c.session.refreshAt = c.now()
		c.mu.Unlock()
		require.NoError(t, c.Warmup(context.Background()))
		<-entered
		c.mu.Lock()
		c.contextExpires = time.Time{}
		c.mu.Unlock()
		waiter := coalescingTokens(c, context.Background())
		synctest.Wait()
		require.Zero(t, wire.calls.Load(), "foreground joins even before the builder dispatches HTTP")
		release()
		<-wire.entered
		synctest.Wait()
		require.Equal(t, int64(1), wire.calls.Load())
		wire.release()
		got := <-waiter
		require.NoError(t, got.err)
		requireSharedSnapshot(t, c, first, got.snapshot)
		require.Equal(t, int64(1), wire.calls.Load())
		require.NoError(t, c.Close())
	})
}
