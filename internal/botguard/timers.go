package botguard

import (
	"context"
	_ "embed"
	"github.com/dop251/goja"
	"time"
)

//go:embed assets/timers.js
var timersJS string

// A generous per-runtime accumulation guard, not a JavaScript heap limit.
// Cleared/fired timeouts free a slot; intervals retain theirs until canceled.
const maxPendingTimers = 65536

type scheduled struct {
	id     int
	repeat bool
	delay  int64
	due    time.Time
	fn     goja.Callable
	args   []goja.Value
}

func (r *runtime) installTimers() {
	vm := r.vm
	schedule := func(repeat bool) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			fn, ok := goja.AssertFunction(call.Argument(0))
			if !ok {
				// The interval surface has already performed DOMString conversion.
				// Keep the existing callable-only timeout policy unchanged.
				if !repeat {
					panic(vm.NewTypeError("invalid timer callback"))
				}
				source := call.Argument(0).String()
				fn = func(goja.Value, ...goja.Value) (goja.Value, error) { return vm.RunString(source) }
			}
			delay := call.Argument(1).ToInteger()
			if delay < 0 {
				delay = 0
			}
			if delay > 10000 {
				delay = 10000
			}
			if r.timerLimit || len(r.timers) >= maxPendingTimers {
				r.timerLimit = true
				panic(vm.NewTypeError("pending timer limit exceeded"))
			}
			r.nextID++
			var args []goja.Value
			if len(call.Arguments) > 2 {
				args = append([]goja.Value(nil), call.Arguments[2:]...)
			}
			r.timers = append(r.timers, scheduled{id: r.nextID, repeat: repeat, delay: delay, due: time.Now().Add(time.Duration(delay) * time.Millisecond), fn: fn, args: args})
			return vm.ToValue(r.nextID)
		}
	}
	vm.Set("setTimeout", schedule(false))
	vm.Set("pocSetInterval", schedule(true))
	vm.Set("clearTimeout", func(id int) {
		for i, t := range r.timers {
			if t.id == id {
				copy(r.timers[i:], r.timers[i+1:])
				r.timers[len(r.timers)-1] = scheduled{}
				r.timers = r.timers[:len(r.timers)-1]
				break
			}
		}
	})
}

func (r *runtime) pump(ctx context.Context, done func() bool) error {
	// Reuse one Go timer rather than allocate one for each idle millisecond.
	wait := time.NewTimer(time.Hour)
	wait.Stop()
	defer wait.Stop()
	for !done() {
		if e := ctx.Err(); e != nil {
			return e
		}
		fired := false
		for i, t := range r.timers {
			ready := !time.Now().Before(t.due)
			if ready {
				copy(r.timers[i:], r.timers[i+1:])
				r.timers[len(r.timers)-1] = scheduled{}
				r.timers = r.timers[:len(r.timers)-1]
				// Keep the interval active during its callback so either clear API,
				// including self-cancellation, can remove it from the shared ID pool.
				if t.repeat {
					r.timers = append(r.timers, t)
				}
				r.timersFired++
				if _, e := t.fn(r.vm.GlobalObject(), t.args...); e != nil {
					r.timerErrors++
					return e
				}
				if t.repeat {
					for j := range r.timers {
						if r.timers[j].id == t.id {
							r.timers[j].due = time.Now().Add(time.Duration(t.delay) * time.Millisecond)
							break
						}
					}
				}
				fired = true
				break
			}
		}
		if !fired {
			wait.Reset(time.Millisecond)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wait.C:
			}
		}
	}
	return nil
}
