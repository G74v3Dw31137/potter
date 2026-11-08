package potter

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type challengeBudget struct {
	homepages, initializations, challenges, interpreters, integrity, mints int64
	interpreterRequests                                                    int64
}

func challengeBudgetClient(t *testing.T) (*Client, *atomic.Int64) {
	t.Helper()
	c := boundaryClient(t, "visitor")
	var homepages atomic.Int64
	base := c.http.Transport
	c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/" {
			homepages.Add(1)
		}
		return base.RoundTrip(r)
	})
	return c, &homepages
}

func requireChallengeBudget(t *testing.T, c *Client, homepages *atomic.Int64, want challengeBudget) {
	t.Helper()
	s := c.engine.Stats()
	require.Equal(t, want, challengeBudget{homepages.Load(), s.RuntimeInitializations,
		s.ChallengeEvaluations, s.InterpreterExecutions, s.GenerateITRequests, s.MintExecutions, s.InterpreterRequests})
}

func requireFixturePair(t *testing.T, s SessionSnapshot, player, gvs string) {
	t.Helper()
	p, err := base64.URLEncoding.DecodeString(s.Tokens().Player)
	require.NoError(t, err)
	g, err := base64.URLEncoding.DecodeString(s.Tokens().GVS)
	require.NoError(t, err)
	require.Equal(t, player, string(p))
	require.Equal(t, gvs, string(g))
}

func TestChallengeBudgetColdRepeatedAndConcurrent(t *testing.T) {
	for _, visitorBinding := range []bool{false, true} {
		t.Run(map[bool]string{false: "video", true: "visitor"}[visitorBinding], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, homepages := challengeBudgetClient(t)
				if visitorBinding {
					rewriteSnapshotPage(c, func(s string) string {
						return strings.ReplaceAll(s, "html5_generate_content_po_token=true", "html5_generate_content_po_token=false")
					}, "")
				}
				var results []<-chan coalescingResult
				for range 12 {
					results = append(results, coalescingTokens(c, context.Background()))
				}
				var first SessionSnapshot
				gvs := "video"
				if visitorBinding {
					gvs = "visitor"
				}
				for _, result := range results {
					got := <-result
					require.NoError(t, got.err)
					require.Equal(t, uint64(1), got.snapshot.Generation())
					requireFixturePair(t, got.snapshot, "fixture:1:video", "fixture:2:"+gvs)
					first = got.snapshot
				}
				want := challengeBudget{1, 1, 1, 1, 1, 2, 1}
				requireChallengeBudget(t, c, homepages, want)
				for range 5 {
					got, err := c.TokensWithSession(context.Background(), "video")
					require.NoError(t, err)
					require.Equal(t, first, got)
				}
				requireChallengeBudget(t, c, homepages, want)
				got, err := c.TokensWithSession(context.Background(), "another")
				require.NoError(t, err)
				want.mints++ // independent Player binding always needs one mint
				if visitorBinding {
					requireFixturePair(t, got, "fixture:3:another", "fixture:2:visitor")
				} else {
					want.mints++ // independent GVS video binding needs its own mint
					requireFixturePair(t, got, "fixture:3:another", "fixture:4:another")
				}
				requireChallengeBudget(t, c, homepages, want)
				require.NoError(t, c.Close())
				requireChallengeBudget(t, c, homepages, want)
			})
		})
	}
}

func TestChallengeBudgetReplacement(t *testing.T) {
	for _, kind := range []string{"refresh", "homepage-revision", "interpreter", "identity", "configuration", "binding"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, homepages := challengeBudgetClient(t)
				first, err := c.TokensWithSession(context.Background(), "video")
				require.NoError(t, err)
				rewriteSnapshotPage(c, func(page string) string {
					switch kind {
					case "interpreter":
						page = strings.Replace(page, "program:'fixture-program'", "interpreterHash:'new',program:'fixture-program'", 1)
					case "identity":
						page = strings.ReplaceAll(page, `"visitor"`, `"replacement"`)
					case "configuration":
						page = strings.Replace(page, `"client":{`, `"client":{"newField":"new",`, 1)
					case "binding":
						page = strings.ReplaceAll(page, "html5_generate_content_po_token=true", "html5_generate_content_po_token=false")
					}
					return page + "<!--new revision-->"
				}, "https://www.youtube.com/?revision=new")
				c.mu.Lock()
				if kind == "refresh" {
					c.session.expires = c.now() // forces a new session, not a caller's cache hit
				} else {
					c.contextExpires = time.Time{}
				}
				c.mu.Unlock()
				got, err := c.TokensWithSession(context.Background(), "video")
				require.NoError(t, err)
				if kind == "interpreter" {
					// A changed interpreter hint schedules a background refresh while
					// the old session can still serve its own cached proofs.
					synctest.Wait()
					got, err = c.TokensWithSession(context.Background(), "video")
					require.NoError(t, err)
				}
				want := challengeBudget{2, 2, 2, 2, 2, 4, 1}
				if kind == "homepage-revision" {
					want = challengeBudget{2, 1, 1, 1, 1, 2, 1}
					require.Equal(t, first, got, "a new page alone does not replace a compatible live minter")
				} else {
					require.Equal(t, first.Generation()+1, got.Generation())
					_, body := readSnapshot(t, got)
					require.Contains(t, string(body), "<!--new revision-->")
					gvs := "video"
					if kind == "binding" {
						gvs = "visitor"
					}
					requireFixturePair(t, got, "fixture:1:video", "fixture:2:"+gvs)
				}
				if kind == "interpreter" {
					want.interpreterRequests = 2
				}
				requireChallengeBudget(t, c, homepages, want)
				require.NoError(t, c.Close())
				_, oldBody := readSnapshot(t, first)
				require.NotContains(t, string(oldBody), "<!--new revision-->")
			})
		})
	}
}

func TestChallengeBudgetEvaluationFailureRetry(t *testing.T) {
	c, homepages := challengeBudgetClient(t)
	var fail atomic.Bool
	fail.Store(true)
	rewriteSnapshotPage(c, func(page string) string {
		if fail.Load() {
			// Tiny discovery succeeds; the initialized session evaluation fails.
			return strings.Replace(page, "program:'fixture-program'", `get program(){if(typeof document==='object')throw Error('PRIVATE');return 'fixture-program'}`, 1)
		}
		return page
	}, "")
	got, err := c.TokensWithSession(context.Background(), "video")
	require.ErrorIs(t, err, ErrChallenge)
	require.NotContains(t, err.Error(), "PRIVATE")
	require.Equal(t, SessionSnapshot{}, got)
	want := challengeBudget{1, 1, 1, 0, 0, 0, 0}
	requireChallengeBudget(t, c, homepages, want)
	require.ErrorIs(t, c.Warmup(context.Background()), ErrChallenge)
	requireChallengeBudget(t, c, homepages, want) // backoff does no work
	fail.Store(false)
	c.mu.Lock()
	c.retryAt = time.Time{} // allow the policy retry without sleeping
	c.mu.Unlock()
	got, err = c.TokensWithSession(context.Background(), "video")
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.Generation())
	requireFixturePair(t, got, "fixture:1:video", "fixture:2:video")
	requireChallengeBudget(t, c, homepages, challengeBudget{2, 2, 2, 1, 1, 2, 1})
}

func TestChallengeBudgetOverlapCancellationAndShutdown(t *testing.T) {
	for _, outcome := range []string{"complete", "cancel-waiter", "cancel-build", "shutdown"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, homepages := challengeBudgetClient(t)
				first, err := c.TokensWithSession(context.Background(), "video")
				require.NoError(t, err)
				entered := make(chan struct{})
				finish, release := boundaryBarrier()
				t.Cleanup(release)
				base := c.http.Transport
				c.http.Transport = homepageTransport(func(r *http.Request) (*http.Response, error) {
					if r.Method == http.MethodPost {
						close(entered) // all JS initialization has completed
						select {
						case <-finish:
						case <-r.Context().Done():
							return nil, r.Context().Err()
						}
					}
					return base.RoundTrip(r)
				})
				c.mu.Lock()
				c.session.refreshAt = c.now()
				c.mu.Unlock()
				require.NoError(t, c.Warmup(context.Background()))
				<-entered
				c.mu.Lock()
				job := c.job
				c.contextExpires = time.Time{}
				c.mu.Unlock()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				waiter := coalescingTokens(c, ctx)
				synctest.Wait()
				want := challengeBudget{2, 2, 2, 2, 2, 2, 1}
				requireChallengeBudget(t, c, homepages, want)
				switch outcome {
				case "cancel-waiter":
					cancel()
					require.ErrorIs(t, (<-waiter).err, context.Canceled)
					require.NoError(t, job.work.Err())
					waiter = coalescingTokens(c, context.Background())
					synctest.Wait()
				case "cancel-build":
					job.cancel()
				case "shutdown":
					require.NoError(t, c.Close())
				}
				release()
				<-job.done
				got := <-waiter
				if outcome == "shutdown" || outcome == "cancel-build" {
					require.Error(t, got.err)
					require.Equal(t, SessionSnapshot{}, got.snapshot)
					require.Equal(t, first.Generation(), c.generation)
				} else {
					require.NoError(t, got.err)
					require.Equal(t, first.Generation()+1, got.snapshot.Generation())
					requireFixturePair(t, got.snapshot, "fixture:1:video", "fixture:2:video")
					want.mints = 4
				}
				require.NoError(t, c.Close())
				requireChallengeBudget(t, c, homepages, want)
				_, err = c.Tokens(context.Background(), "video")
				require.ErrorIs(t, err, ErrClosed)
				requireChallengeBudget(t, c, homepages, want)
			})
		})
	}
}
