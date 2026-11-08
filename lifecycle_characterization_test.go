package potter_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter"
	"github.com/stretchr/testify/require"
)

// synctest's virtual clock and quiescence barriers characterize real public
// operations against the existing offline HTTP/JS fixture, without live traffic.
func calls(t *testing.T, f *fixture, kind string, want int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, want, f.calls[kind], kind)
}

func TestLifecycleInitialization(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(fmt.Sprint(warm), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, f := newFixture(t, "html5_generate_content_po_token=true")
				calls(t, f, "homepage", 0)
				if warm {
					require.NoError(t, c.Warmup(context.Background()))
				}
				var wg sync.WaitGroup
				for i := range 12 {
					wg.Go(func() {
						if i%2 == 0 {
							require.NoError(t, c.Warmup(context.Background()))
						} else {
							_, err := c.Tokens(context.Background(), "video")
							require.NoError(t, err)
						}
					})
				}
				wg.Wait()
				require.NoError(t, c.Warmup(context.Background()))
				calls(t, f, "homepage", 1)
				calls(t, f, "interpreter", 1)
				calls(t, f, "integrity", 1)
				a, err := c.Tokens(context.Background(), "video")
				require.NoError(t, err)
				b, err := c.Tokens(context.Background(), "video")
				require.NoError(t, err)
				require.Equal(t, a, b)
				require.NoError(t, c.Close())
			})
		})
	}
}

func TestLifecycleInitializationFailureRetry(t *testing.T) {
	for _, kind := range []string{"homepage", "interpreter", "integrity"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, f := newFixture(t, "html5_generate_content_po_token=true")
				f.status[kind] = 403
				require.Error(t, c.Warmup(context.Background()))
				f.mu.Lock()
				f.status[kind] = 0
				f.mu.Unlock()
				if kind != "homepage" {
					require.Error(t, c.Warmup(context.Background())) // saved error, no immediate rebuild
					calls(t, f, kind, 1)
					time.Sleep(3 * time.Second) // beyond first backoff including its maximum jitter
				}
				require.NoError(t, c.Warmup(context.Background()))
				calls(t, f, kind, 2)
				require.NoError(t, c.Close())
			})
		})
	}
}

func TestLifecycleRefreshFailureAndRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.integrityReply = `["VEVTVElU",60,30]`
		first, err := c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		f.mu.Lock()
		f.status["integrity"] = 403
		f.mu.Unlock()
		time.Sleep(54 * time.Second) // exact refresh boundary (60 - 60/10)
		got, err := c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		require.Equal(t, first, got)
		synctest.Wait() // background replacement has completed and saved its error
		calls(t, f, "integrity", 2)
		for range 4 {
			got, err = c.Tokens(context.Background(), "video")
			require.NoError(t, err)
			require.Equal(t, first, got)
		}
		calls(t, f, "integrity", 2)
		time.Sleep(6 * time.Second) // exact integrity expiration
		got, err = c.Tokens(context.Background(), "video")
		require.ErrorIs(t, err, potter.ErrExpired)
		require.ErrorIs(t, err, potter.ErrGenerateIT)
		require.Equal(t, potter.Tokens{}, got)
		calls(t, f, "integrity", 3)
		require.Error(t, c.Warmup(context.Background()))
		calls(t, f, "integrity", 3)
		f.mu.Lock()
		f.status["integrity"] = 0
		f.mu.Unlock()
		time.Sleep(6 * time.Second) // beyond second backoff (4..5 seconds)
		got, err = c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		calls(t, f, "integrity", 4)
		calls(t, f, "interpreter", 1)
		require.NoError(t, c.Close())
	})
}

func TestLifecycleReplacementIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.integrityReply = `["VEVTVElU",60,30]`
		_, err := c.Tokens(context.Background(), "first")
		require.NoError(t, err)
		f.mu.Lock()
		f.visitor = "replacement"
		f.page = fixturePage("html5_generate_content_po_token=false", f.visitor)
		f.mu.Unlock()
		time.Sleep(60 * time.Second)
		got, err := c.Tokens(context.Background(), "next")
		require.NoError(t, err)
		require.Equal(t, "replacement", got.VisitorData)
		require.Equal(t, got.VisitorData, got.GVSBinding)
		require.Equal(t, "fixture:1:next", decode(t, got.Player))
		require.Equal(t, "fixture:2:replacement", decode(t, got.GVS))
		calls(t, f, "homepage", 2)
		calls(t, f, "integrity", 2)
		calls(t, f, "interpreter", 1)
		require.NoError(t, c.Close())
	})
}

func TestLifecycleCanceledInitializationAndWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, c.Warmup(ctx), context.Canceled)
		calls(t, f, "homepage", 0)
		f.block = make(chan struct{})
		f.entered = make(chan struct{})
		ctx, cancel = context.WithCancel(context.Background())
		first := make(chan error, 1)
		go func() { first <- c.Warmup(ctx) }()
		<-f.entered
		waiting, stop := context.WithCancel(context.Background())
		queued := make(chan error, 1)
		go func() { _, err := c.Tokens(waiting, "queued"); queued <- err }()
		synctest.Wait()
		stop()
		require.ErrorIs(t, <-queued, context.Canceled)
		cancel()
		require.ErrorIs(t, <-first, context.Canceled)
		// A different caller joins the still-running shared initialization.
		healthy := make(chan error, 1)
		go func() { _, err := c.Tokens(context.Background(), "healthy"); healthy <- err }()
		synctest.Wait()
		calls(t, f, "integrity", 1)
		close(f.block)
		require.NoError(t, <-healthy)
		calls(t, f, "integrity", 1)
		require.NoError(t, c.Close())
	})
}

func TestLifecycleClosePhases(t *testing.T) {
	for _, phase := range []string{"empty", "ready", "initializing", "refreshing", "minting"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, f := newFixture(t, "html5_generate_content_po_token=true")
				// Make a pending mint durably block in the existing JS timer pump.
				f.script = []byte(strings.ReplaceAll(string(f.script), "while(true){}", "await new Promise(function(){});"))
				f.integrityReply = `["VEVTVElU",60,30]`
				if phase == "ready" || phase == "refreshing" || phase == "minting" {
					require.NoError(t, c.Warmup(context.Background()))
				}
				var work chan error
				if phase == "initializing" || phase == "refreshing" {
					f.block = make(chan struct{})
					f.entered = make(chan struct{})
					if phase == "refreshing" {
						time.Sleep(54 * time.Second)
					}
					work = make(chan error, 1)
					go func() { work <- c.Warmup(context.Background()) }()
					<-f.entered
				}
				if phase == "minting" {
					work = make(chan error, 1)
					go func() { _, err := c.Tokens(context.Background(), "hang"); work <- err }()
					synctest.Wait()
				}
				var wg sync.WaitGroup
				for range 6 {
					wg.Go(func() { require.NoError(t, c.Close()) })
				}
				wg.Wait()
				if work != nil {
					err := <-work
					if phase == "refreshing" {
						require.NoError(t, err)
					} else {
						require.Error(t, err)
					}
				}
				require.NoError(t, c.Close())
				require.ErrorIs(t, c.Warmup(context.Background()), potter.ErrClosed)
				_, err := c.Tokens(context.Background(), "video")
				require.ErrorIs(t, err, potter.ErrClosed)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				require.ErrorIs(t, c.Warmup(ctx), context.Canceled)
				synctest.Wait() // the bubble also rejects surviving workers at completion
			})
		})
	}
}

func TestLifecycleMintCancellationIsolation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.script = []byte(strings.ReplaceAll(string(f.script), "while(true){}", "await new Promise(function(){});"))
		require.NoError(t, c.Warmup(context.Background()))
		ctx, cancel := context.WithCancel(context.Background())
		first := make(chan error, 1)
		go func() { _, err := c.Tokens(ctx, "hang"); first <- err }()
		synctest.Wait()
		other := make(chan error, 1)
		go func() { _, err := c.Tokens(context.Background(), "healthy"); other <- err }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-first, context.Canceled)
		require.NoError(t, <-other)
		calls(t, f, "integrity", 2) // canceled runtime retired; other caller rebuilds immediately
		require.NoError(t, c.Close())
	})
}

func TestLifecycleRefreshWaiterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.integrityReply = `["VEVTVElU",60,30]`
		require.NoError(t, c.Warmup(context.Background()))
		f.block = make(chan struct{})
		f.entered = make(chan struct{})
		time.Sleep(54 * time.Second)
		require.NoError(t, c.Warmup(context.Background()))
		<-f.entered
		time.Sleep(6 * time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- c.Warmup(ctx) }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-result, context.Canceled)
		close(f.block)
		require.NoError(t, c.Warmup(context.Background()))
		calls(t, f, "integrity", 2)
		require.NoError(t, c.Close())
	})
}

func TestLifecycleProofCacheLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=false")
		f.integrityReply = `["VEVTVElU",3600,30]`
		a, err := c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		time.Sleep(2 * time.Minute)
		b, err := c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		require.NotEqual(t, a.Player, b.Player)
		require.NotEqual(t, a.GVS, b.GVS)
		calls(t, f, "integrity", 1)
		// A repeated visitor binding stays cached independently of each Player role.
		next, err := c.Tokens(context.Background(), "next")
		require.NoError(t, err)
		require.Equal(t, b.GVS, next.GVS)
		require.NoError(t, c.Close())
	})
}

func TestLifecycleReplacementDuringMint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.script = []byte(strings.ReplaceAll(string(f.script), "while(true){}", "await new Promise(function(resolve){setTimeout(resolve,5)});"))
		f.integrityReply = `["VEVTVElU",60,30]`
		require.NoError(t, c.Warmup(context.Background()))
		f.block = make(chan struct{})
		f.entered = make(chan struct{})
		time.Sleep(54 * time.Second)
		result := make(chan error, 1)
		go func() {
			tokens, err := c.Tokens(context.Background(), "hang")
			if err == nil && (tokens.Player == "" || tokens.GVS == "") {
				err = fmt.Errorf("missing paired proofs")
			}
			result <- err
		}()
		<-f.entered
		synctest.Wait() // old generation is inside its asynchronous mint
		close(f.block)
		synctest.Wait()              // replacement published while the old mint still holds its runtime
		require.NoError(t, <-result) // completion must be safe through replacement
		got, err := c.Tokens(context.Background(), "next")
		require.NoError(t, err)
		require.Equal(t, "fixture:1:next", decode(t, got.Player))
		calls(t, f, "integrity", 2)
		require.NoError(t, c.Close())
	})
}

func TestLifecycleHomepageExpiryAndInterpreterHint(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.integrityReply = `["VEVTVElU",3600,30]`
		require.NoError(t, c.Warmup(context.Background()))
		f.mu.Lock()
		f.page = []byte(strings.Replace(string(f.page), "program:'fixture-program',", "interpreterHash:'replacement',program:'fixture-program',", 1))
		f.mu.Unlock()
		time.Sleep(30 * time.Minute) // homepage cache expires before the integrity session
		require.NoError(t, c.Warmup(context.Background()))
		synctest.Wait()
		got, err := c.Tokens(context.Background(), "video")
		require.NoError(t, err)
		require.Equal(t, "fixture:1:video", decode(t, got.Player))
		calls(t, f, "homepage", 2)
		calls(t, f, "interpreter", 2)
		calls(t, f, "integrity", 2)
		require.NoError(t, c.Close())
	})
}

func TestLifecycleMintFailureBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		got, err := c.Tokens(context.Background(), "reject")
		require.ErrorIs(t, err, potter.ErrMint)
		require.Equal(t, potter.Tokens{}, got)
		require.ErrorIs(t, c.Warmup(context.Background()), potter.ErrMint)
		calls(t, f, "integrity", 1)
		time.Sleep(5 * time.Second)
		got, err = c.Tokens(context.Background(), "healthy")
		require.NoError(t, err)
		require.Equal(t, "fixture:1:healthy", decode(t, got.Player))
		calls(t, f, "integrity", 2)
		require.NoError(t, c.Close())
	})
}

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLifecycleCancellationAtHTTPBoundary(t *testing.T) {
	for _, stage := range []string{"homepage", "interpreter"} {
		for _, shutdown := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/close=%t", stage, shutdown), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					_, f := newFixture(t, "html5_generate_content_po_token=true")
					entered, finished := make(chan struct{}), make(chan struct{})
					var once sync.Once
					c, err := potter.NewClient(potter.WithHTTPClient(&http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
						match := stage == "homepage" && r.URL.Host == "m.youtube.com" || stage == "interpreter" && r.URL.Host == "www.google.com"
						if match {
							once.Do(func() { close(entered) })
							<-r.Context().Done()
							close(finished)
							return nil, r.Context().Err()
						}
						return f.RoundTrip(r)
					})}))
					require.NoError(t, err)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := make(chan error, 1)
					go func() { done <- c.Warmup(ctx) }()
					<-entered
					if shutdown {
						require.NoError(t, c.Close())
						require.Error(t, <-done)
						<-finished
					} else {
						cancel()
						require.ErrorIs(t, <-done, context.Canceled)
						if stage == "homepage" {
							<-finished
						} else {
							// Shared interpreter acquisition survives caller cancellation until Close.
							select {
							case <-finished:
								t.Fatal("caller canceled the shared build")
							default:
							}
						}
						require.NoError(t, c.Close())
						<-finished
					}
					synctest.Wait()
				})
			})
		}
	}
}
