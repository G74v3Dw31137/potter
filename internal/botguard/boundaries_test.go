package botguard

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/require"
)

func TestPromiseSettlement(t *testing.T) {
	for _, tc := range []struct{ name, expression, code string }{
		{"reject-undefined", `Promise.reject(undefined)`, "promise-rejected"},
		{"reject-no-reason", `Promise.reject()`, "promise-rejected"},
		{"reject-private-object", `Promise.reject({get secret(){throw 'PRIVATE'}})`, "promise-rejected"},
		{"fulfill-undefined", `Promise.resolve(undefined)`, "non-string-token"},
		{"timer-reject-undefined", `new Promise(function(_,reject){setTimeout(function(){reject(undefined)},0)})`, "promise-rejected"},
		{"timer-fulfill-undefined", `new Promise(function(resolve){setTimeout(resolve,0)})`, "non-string-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rt := newRuntime(fixtureContext())
				require.NoError(t, rt.eval(`var minter={mintAsWebsafeString(){return `+tc.expression+`}};setTimeout(function(){},10000);`))
				start := time.Now()
				token, err := rt.mint(context.Background(), "binding")
				require.Empty(t, token)
				require.ErrorIs(t, err, ErrMint)
				require.NotErrorIs(t, err, context.DeadlineExceeded)
				require.NotErrorIs(t, err, context.Canceled)
				var classified *Error
				require.ErrorAs(t, err, &classified)
				require.Equal(t, tc.code, classified.Code)
				require.NotContains(t, err.Error(), "PRIVATE")
				require.Equal(t, start, time.Now(), "settled promise must not advance even virtual time")
				require.True(t, rt.broken)
				require.Empty(t, rt.timers, "failure must release timer callbacks")
				_, err = rt.mint(context.Background(), "another")
				require.ErrorAs(t, err, &classified)
				require.Equal(t, "runtime-unusable", classified.Code)
			})
		})
	}
	t.Run("fulfillment-is-settled-before-validation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			start := time.Now()
			require.NoError(t, rt.run(context.Background(), ErrRuntime, func(ctx context.Context) error {
				return rt.promise(ctx, `Promise.resolve(undefined)`, "result")
			}))
			require.True(t, goja.IsUndefined(rt.vm.Get("result")))
			require.False(t, rt.broken)
			require.Equal(t, start, time.Now())
		})
	})
	t.Run("snapshot-rejection", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			require.NoError(t, rt.eval(`var bgClient={snapshot(){return Promise.reject(undefined)}}`))
			snapshot, err := rt.takeSnapshot(context.Background())
			require.Empty(t, snapshot)
			require.ErrorIs(t, err, ErrSnapshot)
			var classified *Error
			require.ErrorAs(t, err, &classified)
			require.Equal(t, "promise-rejected", classified.Code)
		})
	})
}

func TestCompilationCancellation(t *testing.T) {
	for _, cancelAt := range []int{0, 1, len(staticSources())} {
		t.Run(fmt.Sprintf("static-%d", cancelAt), func(t *testing.T) {
			m, f := newFake(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			m.compile = func(name, source string, strict bool) (*goja.Program, error) {
				calls++
				// The test seam controls compiler completion, without expensive inputs or
				// relying on how long the actual synchronous compiler takes.
				p, err := goja.Compile(name, "void 0", strict)
				if calls == cancelAt {
					cancel()
				}
				return p, err
			}
			if cancelAt == 0 {
				cancel()
			}
			handle, err := m.Create(ctx, fixtureContext(), time.Now)
			require.Nil(t, handle)
			require.ErrorIs(t, err, ErrRuntime)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, cancelAt, calls)
			require.Nil(t, m.programs, "partial/canceled static program sets must not be cached")
			require.Zero(t, f.gets.Load())
			require.Zero(t, f.attestations.Load())
		})
	}
	for _, inline := range []bool{false, true} {
		t.Run(fmt.Sprint("interpreter-inline=", inline), func(t *testing.T) {
			m, f := newFake(t)
			_, err := m.staticPrograms(context.Background())
			require.NoError(t, err)
			input := fixtureContext()
			if inline {
				input.Page = []byte(`<script>window.ytAtN({R:{bgChallenge:{program:'fixture-program',globalName:'testGuard',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'void 0'}}}})</script>`)
			}
			entered, finish := make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.compile = func(name, source string, strict bool) (*goja.Program, error) {
				p, err := goja.Compile(name, source, strict)
				close(entered)
				<-finish
				return p, err
			}
			result := make(chan error, 1)
			go func() {
				handle, err := m.Create(ctx, input, time.Now)
				if handle != nil {
					handle.Close()
					result <- fmt.Errorf("canceled compilation returned a session")
					return
				}
				result <- err
			}()
			<-entered
			cancel()
			close(finish)
			err = <-result
			require.ErrorIs(t, err, ErrInterpreter)
			require.ErrorIs(t, err, context.Canceled)
			require.Empty(t, m.interpreters)
			require.Zero(t, f.attestations.Load())
			m.Close()
			require.Nil(t, m.programs)
			require.Nil(t, m.interpreters)
		})
	}
	t.Run("canceled-cache-hits", func(t *testing.T) {
		m, _ := newFake(t)
		_, err := m.staticPrograms(context.Background())
		require.NoError(t, err)
		ch := challenge{}
		ch.InterpreterJavascript.Value = "void 0"
		_, err = m.interpreter(context.Background(), fixtureContext(), ch)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = m.staticPrograms(ctx)
		require.ErrorIs(t, err, context.Canceled)
		p, err := m.interpreter(ctx, fixtureContext(), ch)
		require.Nil(t, p)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestPendingTimerBoundAndCleanup(t *testing.T) {
	for _, kind := range []string{"setTimeout", "setInterval"} {
		t.Run(kind, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			require.NoError(t, rt.eval(timersJS))
			require.NoError(t, rt.vm.Set("limit", maxPendingTimers))
			require.NoError(t, rt.run(context.Background(), ErrRuntime, func(context.Context) error {
				return rt.eval(`var ids=[];for(var i=0;i<limit;i++)ids.push(` + kind + `(function(){},10000));`)
			}))
			require.Len(t, rt.timers, maxPendingTimers)
			// A canceled timer frees a slot; the bound applies to active timers, not
			// the total timers registered over the runtime's lifetime.
			require.NoError(t, rt.run(context.Background(), ErrRuntime, func(context.Context) error {
				return rt.eval(`clearTimeout(ids[0]);` + kind + `(function(){},10000);`)
			}))
			err := rt.run(context.Background(), ErrMint, func(context.Context) error {
				// JS cannot swallow the resource failure and publish a successful result.
				return rt.eval(`try{` + kind + `(function(){},10000)}catch(e){}`)
			})
			require.ErrorIs(t, err, ErrMint)
			var classified *Error
			require.ErrorAs(t, err, &classified)
			require.Equal(t, "timer-limit", classified.Code)
			require.True(t, rt.broken)
			require.Nil(t, rt.timers)
		})
	}
	for _, outcome := range []string{"cancel", "throw", "close"} {
		t.Run(outcome, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			require.NoError(t, rt.eval(`setTimeout(function(){},10000)`))
			if outcome == "close" {
				s := &Session{rt: rt}
				s.Close()
				require.Nil(t, s.rt)
				require.Nil(t, rt.timers)
				return
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if outcome == "cancel" {
				cancel()
			}
			err := rt.run(ctx, ErrMint, func(context.Context) error { return rt.eval(`throw 'PRIVATE'`) })
			require.Error(t, err)
			require.Nil(t, rt.timers)
		})
	}
}

func TestIntegrityExpiryUsesRequestStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, f := newFake(t)
		f.block, f.entered = make(chan struct{}), make(chan struct{})
		type result struct {
			session *Session
			err     error
		}
		finished := make(chan result, 1)
		go func() { s, e := m.Create(context.Background(), fixtureContext(), time.Now); finished <- result{s, e} }()
		<-f.entered
		requestStart := time.Now()
		// Model a slow integrity response using virtual time. None of this delay may
		// be added to the generation's advertised local integrity lifetime.
		time.Sleep(5 * time.Second)
		close(f.block)
		got := <-finished
		require.NoError(t, got.err)
		defer got.session.Close()
		require.Equal(t, requestStart, got.session.Issued)
		require.Equal(t, 60*time.Second, got.session.TTL)
		require.Equal(t, 55*time.Second, time.Until(got.session.Issued.Add(got.session.TTL)))
	})
}

func TestInterpreterCacheKeyAndEviction(t *testing.T) {
	m, _ := newFake(t)
	ch := challenge{InterpreterHash: "same-hash"}
	ch.InterpreterJavascript.Value = "void 1"
	first, err := m.interpreter(context.Background(), fixtureContext(), ch)
	require.NoError(t, err)
	same, err := m.interpreter(context.Background(), fixtureContext(), ch)
	require.NoError(t, err)
	require.Same(t, first, same)
	ch.InterpreterJavascript.Value = "void 2" // identical upstream hint, different bytes
	second, err := m.interpreter(context.Background(), fixtureContext(), ch)
	require.NoError(t, err)
	require.NotSame(t, first, second)
	for i := 3; i <= 5; i++ {
		ch.InterpreterJavascript.Value = fmt.Sprintf("void %d", i)
		_, err = m.interpreter(context.Background(), fixtureContext(), ch)
		require.NoError(t, err)
	}
	require.Len(t, m.interpreters, 4)
	ch.InterpreterJavascript.Value = "void 1"
	afterEviction, err := m.interpreter(context.Background(), fixtureContext(), ch)
	require.NoError(t, err)
	require.NotSame(t, first, afterEviction)
	// An inline interpreter and an external URL never alias, even with the same hint.
	ch.InterpreterJavascript.Value = ""
	ch.InterpreterUrl.Value = "https://www.google.com/botguard/interpreter-v1.js"
	external, err := m.interpreter(context.Background(), fixtureContext(), ch)
	require.NoError(t, err)
	ch.InterpreterHash = "changed-hash"
	changed, err := m.interpreter(context.Background(), fixtureContext(), ch)
	require.NoError(t, err)
	require.NotSame(t, external, changed)
	require.Len(t, m.interpreters, 4)
}
