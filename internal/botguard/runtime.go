package botguard

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"github.com/dop251/goja"
	"strings"
	"time"
)

//go:embed assets/environment.js
var environmentJS string

//go:embed assets/number-format.js
var numberJS string

//go:embed assets/linkedom.js
var domJS string

//go:embed assets/linkedom-adapter.js
var adapterJS string

//go:embed assets/scroll-position.js
var scrollJS string

//go:embed assets/webidl.js
var webIDLJS string

//go:embed assets/image-state.js
var imageStateJS string

//go:embed assets/namespace.js
var namespaceJS string

//go:embed assets/node-position.js
var nodePositionJS string

//go:embed assets/window-surface.js
var windowSurfaceJS string

//go:embed assets/layout-metrics.js
var layoutMetricsJS string

//go:embed assets/collection-history.js
var collectionHistoryJS string

//go:embed assets/bgutils.js
var bgutilsJS string

var errPromiseRejected = errors.New("promise rejected")

type runtime struct {
	interrupted              bool
	vm                       *goja.Runtime
	timers                   []scheduled
	nextID                   int
	broken                   bool
	timerLimit               bool
	timersFired, timerErrors int
}

// try contains Goja host access as well as script execution. Runtime.Try catches
// JS exceptions, but deliberately re-panics Goja's uncatchable engine errors.
// Only those concrete errors are recovered here; Go programming panics propagate.
// Raw engine errors must stay internal and be redacted by the caller.
func (r *runtime) try(fn func() error) (err error) {
	defer func() {
		switch x := recover().(type) {
		case nil:
		case *goja.InterruptedError:
			r.interrupted = true
			err = x
		case *goja.StackOverflowError:
			err = x
		default:
			r.broken = true
			panic(x)
		}
		if err != nil {
			r.broken = true
			var interrupted *goja.InterruptedError
			if errors.As(err, &interrupted) {
				r.interrupted = true
			}
		}
	}()
	if ex := r.vm.Try(func() { err = fn() }); ex != nil {
		err = ex
	}
	return
}

func (r *runtime) run(ctx context.Context, stage error, fn func(context.Context) error) error {
	defer func() {
		if r.broken {
			r.timers = nil
		}
	}()
	if r.broken {
		return &Error{Stage: stage, Code: "runtime-unusable"}
	}
	work, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if e := work.Err(); e != nil {
		r.interrupted = true
		r.broken = true
		return Failure(stage, "canceled", e)
	}
	done := make(chan struct{})
	stop := context.AfterFunc(work, func() { r.vm.Interrupt("canceled"); close(done) })
	var e error
	func() {
		// Stop or join before canceling work, even while a Go panic unwinds.
		defer func() {
			if !stop() {
				<-done
			}
		}()
		e = r.try(func() error { return fn(work) })
	}()
	if work.Err() != nil {
		r.interrupted = true
		r.broken = true
		return Failure(stage, "execution-canceled", work.Err())
	}
	if r.timerLimit {
		r.broken = true
		return &Error{Stage: stage, Code: "timer-limit"}
	}
	if e != nil {
		r.broken = true
		var safe *Error
		if errors.As(e, &safe) && safe.Stage == stage {
			return withSafeCause(safe, e)
		}
		if errors.Is(e, errPromiseRejected) {
			return &Error{Stage: stage, Code: "promise-rejected"}
		}
		return Failure(stage, "javascript", e)
	}
	return nil
}
func (r *runtime) eval(code string) error { _, e := r.vm.RunString(code); return e }
func (r *runtime) promise(ctx context.Context, expression, variable string) error {
	// Settlement is independent of the value/reason, both of which may be
	// undefined. Do not retain or inspect arbitrary rejection reasons.
	if e := r.vm.Set(variable, goja.Undefined()); e != nil {
		return e
	}
	v, e := r.vm.RunString(`Promise.resolve(` + expression + `)`)
	if e != nil {
		return e
	}
	p, ok := v.Export().(*goja.Promise)
	if !ok {
		return errors.New("invalid promise")
	}
	e = r.pump(ctx, func() bool { return p.State() != goja.PromiseStatePending })
	if e != nil {
		return e
	}
	if p.State() == goja.PromiseStateRejected {
		return errPromiseRejected
	}
	return r.vm.Set(variable, p.Result())
}
func newRuntime(c Context) *runtime {
	r := &runtime{vm: goja.New()}
	vm := r.vm
	// These host bindings (including encoding and timers) are installed only on
	// a pristine VM, before scripts can install accessors or readonly globals.
	installEncoding(vm)
	vm.Set("pocResolveDOMURL", resolveDOMURL)
	vm.Set("pocPageURL", "https://m.youtube.com/")
	vm.Set("pocUserAgent", c.UserAgent)
	start := time.Now()
	vm.Set("performanceNow", func() float64 {
		v := float64(time.Since(start).Nanoseconds()) / 1e6
		return v
	})
	vm.Set("print", func(...any) {}) // Never log runtime values or private JS inputs.
	r.installTimers()
	// Initial script execution is guarded by caller's run; constructor only hosts APIs.
	return r
}
func (r *runtime) initialize(ctx context.Context, c Context, programs []*goja.Program) error {
	return r.run(ctx, ErrRuntime, func(ctx context.Context) error {
		for _, p := range programs {
			if _, e := r.vm.RunProgram(p); e != nil {
				return e
			}
		}
		if !json.Valid(c.Config) {
			return &Error{Stage: ErrRuntime, Code: "invalid-config"}
		}
		if e := r.eval("var yt={config_:" + string(c.Config) + "};"); e != nil {
			return e
		}
		return nil
	})
}

type challenge struct {
	Program, GlobalName, InterpreterHash string
	InterpreterUrl                       struct {
		Value string `json:"privateDoNotAccessOrElseTrustedResourceUrlWrappedValue"`
	}
	InterpreterJavascript struct {
		Value string `json:"privateDoNotAccessOrElseSafeScriptWrappedValue"`
	}
}

func (r *runtime) homepageChallenge(ctx context.Context, page []byte) (challenge, bool, error) {
	var c challenge
	var object []byte
	err := VisitPageObjects(page, "window.ytAtN", func(payload []byte) bool {
		object = payload
		return false // The first object is authoritative, regardless of suffix errors.
	})
	if err != nil {
		return c, true, err
	}
	if object == nil {
		return c, false, nil
	}

	e := r.run(ctx, ErrChallenge, func(ctx context.Context) error {
		v, e := r.vm.RunString("(" + string(object) + ")")
		if e != nil {
			return e
		}
		if e := r.vm.Set("attPayload", v); e != nil {
			return e
		}
		v, e = r.vm.RunString(`(function(p){if(typeof p.R==="string")p.R=JSON.parse(p.R);return p.R.bgChallenge})(attPayload)`)
		if e != nil {
			return e
		}
		data, e := json.Marshal(v.Export())
		if e != nil {
			return &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
		}
		if json.Unmarshal(data, &c) != nil {
			return &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
		}
		return nil
	})
	return c, true, e
}
func validChallenge(c challenge) bool {
	return c.Program != "" && c.GlobalName != "" && (c.InterpreterUrl.Value != "" || c.InterpreterJavascript.Value != "")
}
func (r *runtime) snapshot(ctx context.Context, c challenge, interpreter *goja.Program) (string, error) {
	if e := r.loadBotGuard(ctx, c, interpreter); e != nil {
		return "", e
	}
	return r.takeSnapshot(ctx)
}
func (r *runtime) loadBotGuard(ctx context.Context, c challenge, interpreter *goja.Program) error {
	return r.run(ctx, ErrRuntime, func(ctx context.Context) error {
		if _, e := r.vm.RunProgram(interpreter); e != nil {
			return e
		}
		if e := r.vm.Set("challengeProgram", c.Program); e != nil {
			return e
		}
		if e := r.vm.Set("challengeGlobal", c.GlobalName); e != nil {
			return e
		}
		return r.promise(ctx, `BG.BotGuardClient.create({program:challengeProgram,globalName:challengeGlobal,globalObject:globalThis})`, "bgClient")
	})
}

func (r *runtime) takeSnapshot(ctx context.Context) (string, error) {
	var snapshot string
	e := r.run(ctx, ErrSnapshot, func(ctx context.Context) error {
		if e := r.eval(`var signals=[];`); e != nil {
			return e
		}
		if e := r.promise(ctx, `bgClient.snapshot({webPoSignalOutput:signals},3000)`, "snapshot"); e != nil {
			return e
		}
		v := r.vm.Get("snapshot")
		if _, ok := v.Export().(string); !ok {
			return &Error{Stage: ErrSnapshot, Code: "non-string-snapshot"}
		}
		snapshot = v.String()
		if snapshot == "" {
			return &Error{Stage: ErrSnapshot, Code: "empty-snapshot"}
		}
		if strings.HasPrefix(snapshot, "E:") {
			return &Error{Stage: ErrSnapshot, Code: "opaque-error-snapshot"}
		}
		signal := r.vm.Get("signals").ToObject(r.vm).Get("0")
		if _, ok := goja.AssertFunction(signal); !ok {
			return &Error{Stage: ErrSnapshot, Code: "missing-minter-signal"}
		}
		return nil
	})
	return snapshot, e
}
func (r *runtime) minter(ctx context.Context, token string, ttl, threshold float64) error {
	return r.run(ctx, ErrMint, func(ctx context.Context) error {
		if e := r.vm.Set("integrityData", map[string]any{"integrityToken": token, "estimatedTtlSecs": ttl, "mintRefreshThreshold": threshold}); e != nil {
			return e
		}
		return r.promise(ctx, `BG.WebPoMinter.create(integrityData,signals)`, "minter")
	})
}
func (r *runtime) mint(ctx context.Context, binding string) (string, error) {
	var token string
	e := r.run(ctx, ErrMint, func(ctx context.Context) error {
		if e := r.vm.Set("binding", binding); e != nil {
			return e
		}
		if e := r.promise(ctx, `minter.mintAsWebsafeString(binding)`, "mintedToken"); e != nil {
			return e
		}
		v := r.vm.Get("mintedToken")
		if _, ok := v.Export().(string); !ok {
			return &Error{Stage: ErrMint, Code: "non-string-token"}
		}
		token = v.String()
		if token == "" {
			return &Error{Stage: ErrMint, Code: "empty-token"}
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	return token, nil
}

type staticSource struct{ Name, Source string }

// This is the actual execution order as well as the provenance installation list.
func staticSources() []staticSource {
	return []staticSource{{"environment", environmentJS}, {"number-format", numberJS}, {"linkedom", domJS}, {"linkedom-adapter", adapterJS}, {"scroll-position", scrollJS}, {"webidl", webIDLJS}, {"image-state", imageStateJS}, {"namespace", namespaceJS}, {"node-position", nodePositionJS}, {"window-surface", windowSurfaceJS}, {"layout-metrics", layoutMetricsJS}, {"collection-history", collectionHistoryJS}, {"timers", timersJS}, {"bgutils", bgutilsJS}}
}
