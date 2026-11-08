package potter_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter"
	"github.com/stretchr/testify/require"
)

// Compile-time compatibility: the original five-field struct stays comparable
// and supports existing unkeyed literals. Both methods retain their signatures.
var (
	_                                                                               = map[potter.Tokens]bool{{"p", "g", "b", "v", "c"}: true}
	_ func(*potter.Client, context.Context, string) (potter.Tokens, error)          = (*potter.Client).Tokens
	_ func(*potter.Client, context.Context, string) (potter.SessionSnapshot, error) = (*potter.Client).TokensWithSession
)

func snapshotBody(t *testing.T, s potter.SessionSnapshot) (string, []byte) {
	t.Helper()
	u, r := s.Homepage()
	require.NotNil(t, r)
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return u, b
}

func emptySnapshot(t *testing.T, s potter.SessionSnapshot) {
	t.Helper()
	require.Equal(t, potter.Tokens{}, s.Tokens())
	require.Zero(t, s.Generation())
	u, b := snapshotBody(t, s)
	require.Empty(t, u)
	require.Empty(t, b)
}

func TestSessionSnapshotZero(t *testing.T) { emptySnapshot(t, potter.SessionSnapshot{}) }

func TestSessionSnapshotBindingsAndBudget(t *testing.T) {
	for _, visitorBound := range []bool{false, true} {
		for _, oldFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("visitor=%t/old-first=%t", visitorBound, oldFirst), func(t *testing.T) {
				c, f := newFixture(t, fmt.Sprintf("html5_generate_content_po_token=%t", !visitorBound))
				original := append([]byte(nil), f.page...)
				var old potter.Tokens
				if oldFirst {
					var err error
					old, err = c.Tokens(context.Background(), "video")
					require.NoError(t, err)
				}
				s, err := c.TokensWithSession(context.Background(), "video")
				require.NoError(t, err)
				if !oldFirst {
					old, err = c.Tokens(context.Background(), "video")
					require.NoError(t, err)
				}
				require.Equal(t, old, s.Tokens())
				require.Equal(t, uint64(1), s.Generation())
				binding := "video"
				if visitorBound {
					binding = "fixture-visitor"
				}
				require.Equal(t, potter.Tokens{Player: old.Player, GVS: old.GVS, GVSBinding: binding, VisitorData: "fixture-visitor", ClientVersion: "fixture-version"}, s.Tokens())
				require.Equal(t, "fixture:1:video", decode(t, old.Player))
				require.Equal(t, "fixture:2:"+binding, decode(t, old.GVS))
				u, b := snapshotBody(t, s)
				require.Equal(t, "https://m.youtube.com/", u)
				require.Equal(t, original, b)
				// Copies and caller-owned buffers cannot change stored values.
				copy := s.Tokens()
				copy.Player = "changed"
				b[0] ^= 0xff
				_, b = snapshotBody(t, s)
				require.Equal(t, original, b)
				require.Equal(t, old, s.Tokens())
				_, a := s.Homepage()
				_, z := s.Homepage()
				one := make([]byte, 1)
				_, err = a.Read(one)
				require.NoError(t, err)
				whole, err := io.ReadAll(z)
				require.NoError(t, err)
				require.Equal(t, original, whole)
				var wg sync.WaitGroup
				for range 12 {
					wg.Go(func() { _, b := snapshotBody(t, s); require.Equal(t, original, b) })
				}
				wg.Wait()
				for range 4 {
					next, err := c.TokensWithSession(context.Background(), "video")
					require.NoError(t, err)
					require.Equal(t, old, next.Tokens())
					require.Equal(t, s.Generation(), next.Generation())
				}
				// Only one new Player mint is needed for another video; visitor GVS
				// remains cached, while video GVS needs its own binding.
				next, err := c.TokensWithSession(context.Background(), "next")
				require.NoError(t, err)
				require.Equal(t, "fixture:3:next", decode(t, next.Tokens().Player))
				if visitorBound {
					require.Equal(t, old.GVS, next.Tokens().GVS)
				} else {
					require.Equal(t, "fixture:4:next", decode(t, next.Tokens().GVS))
				}
				calls(t, f, "homepage", 1)
				calls(t, f, "interpreter", 1)
				calls(t, f, "integrity", 1)
				for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
					require.Equal(t, "potter.SessionSnapshot{redacted}", fmt.Sprintf(format, s))
				}
				require.NoError(t, c.Close())
				_, b = snapshotBody(t, s)
				require.Equal(t, original, b)
			})
		}
	}
}

func TestSessionSnapshotAfterReplacementAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.integrityReply = `["VEVTVElU",60,30]`
		first, err := c.TokensWithSession(context.Background(), "video")
		require.NoError(t, err)
		u, original := snapshotBody(t, first)
		_, reader := first.Homepage() // keep a reader too, without reading it yet
		f.mu.Lock()
		f.visitor = "replacement"
		f.page = fixturePage("html5_generate_content_po_token=false", f.visitor)
		replacement := append([]byte(nil), f.page...)
		f.mu.Unlock()
		time.Sleep(time.Minute)
		next, err := c.TokensWithSession(context.Background(), "next")
		require.NoError(t, err)
		require.Equal(t, uint64(2), next.Generation())
		require.Equal(t, "replacement", next.Tokens().VisitorData)
		require.Equal(t, "replacement", next.Tokens().GVSBinding)
		_, b := snapshotBody(t, next)
		require.Equal(t, replacement, b)
		_, b = snapshotBody(t, first)
		require.Equal(t, original, b)
		var wg sync.WaitGroup
		for range 6 {
			wg.Go(func() { require.NoError(t, c.Close()) })
		}
		wg.Wait()
		u2, b := snapshotBody(t, first)
		require.Equal(t, u, u2)
		require.Equal(t, original, b)
		b, err = io.ReadAll(reader)
		require.NoError(t, err)
		require.Equal(t, original, b)
		_, b = snapshotBody(t, next)
		require.Equal(t, replacement, b)
	})
}

func TestSessionSnapshotFailures(t *testing.T) {
	for _, kind := range []string{"homepage", "interpreter", "integrity", "mint", "empty", "canceled", "closed"} {
		t.Run(kind, func(t *testing.T) {
			c, f := newFixture(t, "html5_generate_content_po_token=true")
			ctx := context.Background()
			video := "video"
			want := map[string]error{"homepage": potter.ErrChallenge, "interpreter": potter.ErrInterpreter, "integrity": potter.ErrGenerateIT, "mint": potter.ErrMint, "empty": potter.ErrBinding, "canceled": context.Canceled, "closed": potter.ErrClosed}[kind]
			switch kind {
			case "homepage", "interpreter", "integrity":
				f.status[kind] = 403
			case "mint":
				video = "reject"
			case "empty":
				video = ""
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "closed":
				require.NoError(t, c.Close())
			}
			s, err := c.TokensWithSession(ctx, video)
			require.ErrorIs(t, err, want)
			emptySnapshot(t, s)
			old, err := c.Tokens(ctx, video)
			require.ErrorIs(t, err, want)
			require.Equal(t, potter.Tokens{}, old)
		})
	}
}

func TestSessionSnapshotCancellationAndClose(t *testing.T) {
	for _, phase := range []string{"homepage", "initializing", "minting"} {
		for _, shutdown := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/close=%t", phase, shutdown), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					base, f := newFixture(t, "html5_generate_content_po_token=true")
					c := base
					entered := make(chan struct{})
					if phase == "homepage" {
						var err error
						c, err = potter.NewClient(potter.WithHTTPClient(&http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
							if r.URL.Path == "/" {
								close(entered)
								<-r.Context().Done()
								return nil, r.Context().Err()
							}
							return f.RoundTrip(r)
						})}))
						require.NoError(t, err)
						defer c.Close()
					} else if phase == "initializing" {
						f.block = make(chan struct{})
						f.entered = entered
					} else {
						f.script = []byte(strings.ReplaceAll(string(f.script), "while(true){}", "await new Promise(function(){});"))
						require.NoError(t, c.Warmup(context.Background()))
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := make(chan error, 1)
					go func() {
						video := "video"
						if phase == "minting" {
							video = "hang"
						}
						s, err := c.TokensWithSession(ctx, video)
						emptySnapshot(t, s)
						done <- err
					}()
					if phase == "minting" {
						synctest.Wait()
					} else {
						<-entered
					}
					queued, stop := context.WithCancel(context.Background())
					queue := make(chan error, 1)
					go func() { s, err := c.TokensWithSession(queued, "queued"); emptySnapshot(t, s); queue <- err }()
					synctest.Wait()
					stop()
					require.ErrorIs(t, <-queue, context.Canceled)
					if shutdown {
						require.NoError(t, c.Close())
					} else {
						cancel()
					}
					err := <-done
					if shutdown {
						require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, potter.ErrClosed), "unexpected shutdown error: %v", err)
					} else {
						require.ErrorIs(t, err, context.Canceled)
					}
					if phase == "initializing" && !shutdown {
						close(f.block) // caller cancellation must leave the shared builder usable
						s, err := c.TokensWithSession(context.Background(), "healthy")
						require.NoError(t, err)
						require.NotZero(t, s.Generation())
						calls(t, f, "integrity", 1)
					}
					require.NoError(t, c.Close())
				})
			})
		}
	}
}

func TestSessionSnapshotFailedReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		f.integrityReply = `["VEVTVElU",60,30]`
		first, err := c.TokensWithSession(context.Background(), "video")
		require.NoError(t, err)
		f.mu.Lock()
		f.status["integrity"] = 403
		f.page = append(f.page, []byte("<!--new page-->")...)
		f.mu.Unlock()
		time.Sleep(54 * time.Second)
		got, err := c.TokensWithSession(context.Background(), "video")
		require.NoError(t, err)
		synctest.Wait()
		require.Equal(t, first.Tokens(), got.Tokens())
		require.Equal(t, first.Generation(), got.Generation())
		_, a := snapshotBody(t, first)
		_, b := snapshotBody(t, got)
		require.Equal(t, a, b)
		time.Sleep(6 * time.Second)
		got, err = c.TokensWithSession(context.Background(), "video")
		require.ErrorIs(t, err, potter.ErrExpired)
		require.ErrorIs(t, err, potter.ErrGenerateIT)
		emptySnapshot(t, got)
	})
}

// Compiled example only; no network work is executed by go test.
func ExampleClient_TokensWithSession() {
	client, err := potter.NewClient()
	if err != nil {
		return
	}
	defer client.Close()
	snapshot, err := client.TokensWithSession(context.Background(), "jNQXAC9IVRw")
	if err != nil {
		return
	}
	tokens := snapshot.Tokens()
	generation := snapshot.Generation()
	documentURL, homepage := snapshot.Homepage()
	// Consumers can parse the homepage using their own logic.
	// Potter does not perform video extraction.
	_, _, _, _ = tokens, generation, documentURL, homepage
}

func TestSessionSnapshotMixedColdConcurrency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, f := newFixture(t, "html5_generate_content_po_token=true")
		var wg sync.WaitGroup
		for i := range 12 {
			wg.Go(func() {
				if i%2 == 0 {
					s, err := c.TokensWithSession(context.Background(), "video")
					require.NoError(t, err)
					require.Equal(t, uint64(1), s.Generation())
					require.Equal(t, "fixture:1:video", decode(t, s.Tokens().Player))
				} else {
					tokens, err := c.Tokens(context.Background(), "video")
					require.NoError(t, err)
					require.Equal(t, "fixture:1:video", decode(t, tokens.Player))
				}
			})
		}
		wg.Wait()
		calls(t, f, "homepage", 1)
		calls(t, f, "interpreter", 1)
		calls(t, f, "integrity", 1)
	})
}

func TestSessionSnapshotGVSFailureReturnsNoPartialPair(t *testing.T) {
	c, f := newFixture(t, "html5_generate_content_po_token=false")
	// Player succeeds; visitor-bound GVS fails in the very same minter.
	f.script = []byte(strings.ReplaceAll(string(f.script), "text==='reject'", "text==='fixture-visitor'"))
	s, err := c.TokensWithSession(context.Background(), "video")
	require.ErrorIs(t, err, potter.ErrMint)
	emptySnapshot(t, s)
}
