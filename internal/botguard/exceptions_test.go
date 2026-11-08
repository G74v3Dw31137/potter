package botguard

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/require"
)

// Recover only in the test harness so the unfixed code reports a test failure
// instead of terminating the process (including for interruption panics).
func containTestPanic(fn func() error) (err error, escaped any) {
	defer func() { escaped = recover() }()
	err = fn()
	return
}

func requireContainedError(t *testing.T, fn func() error, stage error) {
	t.Helper()
	err, escaped := containTestPanic(fn)
	require.Nil(t, escaped, "Goja panic escaped: %T", escaped)
	require.ErrorIs(t, err, stage)
	if stage != context.Canceled {
		require.False(t, errors.Is(err, context.Canceled))
		require.False(t, errors.Is(err, context.DeadlineExceeded))
		// Classification and redaction must survive without exposing source,
		// thrown values, or raw Goja exception objects.
		var classified *Error
		require.ErrorAs(t, err, &classified)
		require.Equal(t, stage, classified.Stage)
		require.NotEmpty(t, classified.Code)
		require.NotContains(t, err.Error(), "PRIVATE")
		require.NotContains(t, err.Error(), "https://")
		var js *goja.Exception
		require.False(t, errors.As(err, &js))
	}
}

func TestExceptionChallengeExport(t *testing.T) {
	page := []byte(`<script>window.ytAtN({R:{bgChallenge:{get program(){throw Error('PRIVATE challenge https://secret.invalid/?visitor=private')}}}})</script>`)
	for _, describe := range []bool{false, true} {
		name := "runtime"
		if describe {
			name = "describe"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requireContainedError(t, func() error {
				if describe {
					_, err := DescribeInterpreter(ctx, page)
					return err
				}
				rt := newRuntime(fixtureContext())
				_, _, err := rt.homepageChallenge(ctx, page)
				return err
			}, ErrChallenge)
		})
	}
}

func TestExceptionRuntimeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, setup string
		stage       error
		run         func(*testing.T, *runtime, context.Context) error
	}{
		{"global-get", `Object.defineProperty(globalThis,'hostValue',{get(){throw 'PRIVATE getter'}})`, ErrRuntime,
			func(t *testing.T, rt *runtime, ctx context.Context) error {
				return rt.run(ctx, ErrRuntime, func(context.Context) error { rt.vm.Get("hostValue"); return nil })
			}},
		{"host-export", `var inspected={get value(){throw 'PRIVATE export'}}`, ErrRuntime,
			func(t *testing.T, rt *runtime, ctx context.Context) error {
				return rt.run(ctx, ErrRuntime, func(context.Context) error { rt.vm.Get("inspected").Export(); return nil })
			}},
		{"host-set", `Object.defineProperty(globalThis,'observer',{set(){throw 'PRIVATE setter'}})`, ErrRuntime,
			func(t *testing.T, rt *runtime, ctx context.Context) error {
				return rt.run(ctx, ErrRuntime, func(context.Context) error { return rt.vm.Set("observer", "fixture") })
			}},
		{"snapshot-signal-get", `var bgClient={snapshot(options){
			Object.defineProperty(options.webPoSignalOutput,'0',{get(){throw Error('PRIVATE signal')}});
			return Promise.resolve('fixture-snapshot');
		}}`, ErrSnapshot, func(t *testing.T, rt *runtime, ctx context.Context) error { _, err := rt.takeSnapshot(ctx); return err }},
		{"snapshot-export", `var bgClient={snapshot(){return Promise.resolve({get value(){throw 'PRIVATE snapshot'}})}}`,
			ErrSnapshot, func(t *testing.T, rt *runtime, ctx context.Context) error { _, err := rt.takeSnapshot(ctx); return err }},
		{"mint-export", `var minter={mintAsWebsafeString(){return Promise.resolve({get value(){throw 'PRIVATE integrity token'}})}}`,
			ErrMint, func(t *testing.T, rt *runtime, ctx context.Context) error {
				token, err := rt.mint(ctx, "PRIVATE visitor")
				require.Empty(t, token)
				return err
			}},
		{"mint-throw", `var minter={mintAsWebsafeString(){throw Error('PRIVATE mint')}}`,
			ErrMint, func(t *testing.T, rt *runtime, ctx context.Context) error {
				_, err := rt.mint(ctx, "binding")
				return err
			}},
		{"mint-reject", `var minter={mintAsWebsafeString(){return Promise.reject('PRIVATE mint')}}`,
			ErrMint, func(t *testing.T, rt *runtime, ctx context.Context) error {
				_, err := rt.mint(ctx, "binding")
				return err
			}},
		{"mint-binding-set", `Object.defineProperty(globalThis,'binding',{set(){throw 'PRIVATE setter'}});
			var minter={mintAsWebsafeString(){return Promise.resolve('must-not-return')}}`,
			ErrMint, func(t *testing.T, rt *runtime, ctx context.Context) error {
				token, err := rt.mint(ctx, "PRIVATE visitor")
				require.Empty(t, token)
				return err
			}},
		{"host-callback-conversion", ``, ErrRuntime, func(t *testing.T, rt *runtime, ctx context.Context) error {
			return rt.run(ctx, ErrRuntime, func(context.Context) error {
				return rt.eval(`setTimeout(function(){}, {valueOf(){throw 'PRIVATE delay'}})`)
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			require.NoError(t, rt.eval(tc.setup))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requireContainedError(t, func() error { return tc.run(t, rt, ctx) }, tc.stage)
			require.True(t, rt.broken)
			require.False(t, rt.interrupted)
		})
	}
}

func TestExceptionInitialization(t *testing.T) {
	for name, source := range map[string]string{
		"throw":       `throw Error('PRIVATE initialization')`,
		"host-setter": `Object.defineProperty(globalThis,'yt',{set(){throw 'PRIVATE initialization setter'}})`,
	} {
		t.Run(name, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			p, err := goja.Compile("private-source.js", source, false)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requireContainedError(t, func() error {
				return rt.initialize(ctx, fixtureContext(), []*goja.Program{p})
			}, ErrRuntime)
		})
	}
}

func TestExceptionHostInspectionInterrupted(t *testing.T) {
	for _, export := range []bool{false, true} {
		name := "get"
		if export {
			name = "export"
		}
		t.Run(name, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, rt.vm.Set("cancelInspection", cancel))
			require.NoError(t, rt.eval(`var inspected={get value(){cancelInspection();while(true){}}}`))
			obj := rt.vm.Get("inspected").ToObject(rt.vm)
			requireContainedError(t, func() error {
				return rt.run(ctx, ErrRuntime, func(context.Context) error {
					if export {
						obj.Export()
					} else {
						obj.Get("value")
					}
					return nil
				})
			}, context.Canceled)
			require.True(t, rt.interrupted)
			require.True(t, rt.broken)
			called := false
			require.ErrorIs(t, rt.run(context.Background(), ErrMint, func(context.Context) error { called = true; return nil }), ErrMint)
			require.False(t, called, "interrupted runtime reused")
		})
	}
}

func TestExceptionHostInspectionEngineErrors(t *testing.T) {
	for _, interrupt := range []bool{false, true} {
		name := "stack-overflow"
		if interrupt {
			name = "interrupt-without-cancellation"
		}
		t.Run(name, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			rt.vm.SetMaxCallStackSize(32)
			require.NoError(t, rt.eval(`var inspected={get value(){return this.value}}`))
			obj := rt.vm.Get("inspected").ToObject(rt.vm)
			if interrupt {
				rt.vm.Interrupt("PRIVATE interrupt reason")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requireContainedError(t, func() error {
				return rt.run(ctx, ErrRuntime, func(context.Context) error { obj.Get("value"); return nil })
			}, ErrRuntime)
			require.True(t, rt.broken)
			require.Equal(t, interrupt, rt.interrupted)
		})
	}
}

func TestExceptionGoPanicAndWatchdogCleanup(t *testing.T) {
	for _, inJS := range []bool{false, true} {
		name := "host"
		if inJS {
			name = "callback"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rt := newRuntime(fixtureContext())
				bug := errors.New("ordinary Go programming bug")
				require.NoError(t, rt.vm.Set("hostBug", func() { panic(bug) }))
				var work context.Context
				_, escaped := containTestPanic(func() error {
					return rt.run(context.Background(), ErrRuntime, func(ctx context.Context) error {
						work = ctx
						if inJS {
							return rt.eval(`hostBug()`)
						}
						panic(bug)
					})
				})
				require.Same(t, bug, escaped, "ordinary Go panic was swallowed")
				require.ErrorIs(t, work.Err(), context.Canceled, "execution context still active")
				// The watchdog must have been stopped or joined before run exits.
				// Wait for all cancellation work to quiesce without wall-clock sleeps.
				// This direct VM access is test-only.
				synctest.Wait()
				_, err := rt.vm.RunString(`1`)
				require.NoError(t, err, "watchdog interrupted the VM after panic unwinding")
			})
		})
	}
}

func TestExceptionWatchdogExitPaths(t *testing.T) {
	for _, outcome := range []string{"success", "exception", "returned-error", "deadline"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rt := newRuntime(fixtureContext())
				require.NoError(t, rt.eval(`var inspected={get value(){throw 'PRIVATE watchdog'}}`))
				var work context.Context
				err, escaped := containTestPanic(func() error {
					return rt.run(context.Background(), ErrRuntime, func(ctx context.Context) error {
						work = ctx
						switch outcome {
						case "exception":
							rt.vm.Get("inspected").ToObject(rt.vm).Get("value")
						case "returned-error":
							return context.Canceled
						case "deadline":
							// synctest advances to the watchdog deadline without
							// wall-clock waiting; returning a value cannot hide it.
							<-ctx.Done()
						}
						return nil
					})
				})
				require.Nil(t, escaped)
				if outcome == "success" {
					require.NoError(t, err)
				} else if outcome == "deadline" {
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.True(t, rt.interrupted)
				} else {
					require.ErrorIs(t, err, ErrRuntime)
					if outcome == "returned-error" {
						require.ErrorIs(t, err, context.Canceled)
					}
				}
				require.Error(t, work.Err(), "execution context still active")
				synctest.Wait()
				if outcome != "deadline" {
					_, err = rt.vm.RunString(`1`)
					require.NoError(t, err, "watchdog work survived the operation")
				}
			})
		})
	}
}

func TestExceptionCanceledMintReturnsNoToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := newRuntime(fixtureContext())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, rt.vm.Set("cancelMint", cancel))
		require.NoError(t, rt.eval(`
		var result, reads=0;
		Object.defineProperty(globalThis,'mintedToken',{
		 get(){if(++reads===1)cancelMint();return result}, set(v){result=v}
		});
		var minter={mintAsWebsafeString(){return Promise.resolve('PRIVATE partial proof')}};
		`))
		var token string
		requireContainedError(t, func() error {
			var err error
			token, err = rt.mint(ctx, "binding")
			return err
		}, context.Canceled)
		require.Empty(t, token)
		require.True(t, rt.broken)
		require.True(t, rt.interrupted)
		synctest.Wait()
	})
}
