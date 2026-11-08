package botguard

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

type Binding uint8

const (
	UnknownBinding Binding = iota
	VideoBinding
	VisitorBinding
)

type Context struct {
	InterpreterVersion string
	Key                string
	Revision           uint64
	CreatedAt          time.Time
	// Page is owned by acquisition and remains immutable for its entire lifetime.
	Page               []byte
	DocumentURL        string
	Config             json.RawMessage
	Visitor, UserAgent string
	Binding            Binding
}

// Stats reports execution requests only; lifecycle accounting belongs to Client.
type Stats struct{ InterpreterRequests, GenerateITRequests int64 }
type metrics struct{ interpreter, integrity atomic.Int64 }

func (m *Engine) Stats() Stats {
	return Stats{m.stats.interpreter.Load(), m.stats.integrity.Load()}
}

type interpreter struct {
	key     string
	program *goja.Program
}

// Engine compiles BotGuard programs and performs protocol initialization.
// Its owner serializes Create and closes it after all builders have joined.
// Returned Sessions belong exclusively to the caller, independently of Engine.
type Engine struct {
	http         *http.Client
	programs     []*goja.Program
	interpreters []interpreter
	stats        metrics
	compile      func(string, string, bool) (*goja.Program, error) // synchronous compiler; test barrier
	// MintStarted is an offline fixture barrier. Public clients never set it;
	// only the synthetic interpreter knows the optional fixture callback.
	MintStarted func()
}

func NewEngine(hc *http.Client) *Engine {
	return &Engine{http: hc, compile: goja.Compile}
}
func (m *Engine) Close() {
	m.programs = nil
	m.interpreters = nil
}

// Session is an initialized runtime/minter handle, with immutable protocol facts.
// The owner must serialize Mint and Close. It makes no reuse/expiration decisions.
type Session struct {
	rt      *runtime
	Issued  time.Time
	TTL     time.Duration
	Version string
}

func (s *Session) Mint(ctx context.Context, binding string) (string, error) {
	if s.rt == nil {
		return "", ErrClosed
	}
	return s.rt.mint(ctx, binding)
}

// Close releases Go-managed runtime state, including pending JS timer callbacks.
// No additional remote JavaScript executes during cleanup.
func (s *Session) Close() {
	if s.rt != nil {
		s.rt.timers = nil
		s.rt = nil
	}
}

// compileProgram cannot interrupt Goja's synchronous compiler. Cancellation is
// checked on both sides so canceled results cannot enter caches or execution.
func (m *Engine) compileProgram(ctx context.Context, stage error, name, source string) (*goja.Program, error) {
	if e := ctx.Err(); e != nil {
		return nil, Failure(stage, "compilation-canceled", e)
	}
	p, e := m.compile(name, source, false)
	if canceled := ctx.Err(); canceled != nil {
		return nil, Failure(stage, "compilation-canceled", canceled)
	}
	if e != nil {
		return nil, &Error{Stage: stage, Code: "javascript-compile"}
	}
	return p, nil
}

func (m *Engine) staticPrograms(ctx context.Context) ([]*goja.Program, error) {
	if e := ctx.Err(); e != nil {
		return nil, Failure(ErrRuntime, "compilation-canceled", e)
	}
	if m.programs != nil {
		return m.programs, nil
	}
	var programs []*goja.Program
	for _, source := range staticSources() {
		p, e := m.compileProgram(ctx, ErrRuntime, "botguard-bundle.js", source.Source)
		if e != nil {
			return nil, e
		}
		programs = append(programs, p)
	}
	m.programs = programs
	return m.programs, nil
}
func (m *Engine) interpreter(ctx context.Context, c Context, ch challenge) (*goja.Program, error) {
	if e := ctx.Err(); e != nil {
		return nil, Failure(ErrInterpreter, "compilation-canceled", e)
	}
	raw := ch.InterpreterJavascript.Value
	key, e := interpreterID(ch)
	if e != nil {
		return nil, e
	}
	for _, entry := range m.interpreters {
		if entry.key == key {
			return entry.program, nil
		}
	}
	if raw == "" {
		u, _ := url.Parse(ch.InterpreterUrl.Value)
		if u.Scheme == "" {
			u.Scheme = "https"
		}
		m.stats.interpreter.Add(1)
		data, e := m.request(ctx, c, "GET", u.String(), nil, ErrInterpreter)
		if e != nil {
			return nil, e
		}
		raw = string(data)
	}
	program, e := m.compileProgram(ctx, ErrInterpreter, "botguard-interpreter.js", raw)
	if e != nil {
		return nil, e
	}
	if len(m.interpreters) >= 4 {
		copy(m.interpreters, m.interpreters[1:])
		m.interpreters = m.interpreters[:3]
	}
	m.interpreters = append(m.interpreters, interpreter{key: key, program: program})
	return program, nil
}

// Create returns only a fully initialized minter. now supplies the coordinator's
// clock for the conservative GenerateIT request-start timestamp.
func (m *Engine) Create(ctx context.Context, c Context, now func() time.Time) (*Session, error) {
	programs, e := m.staticPrograms(ctx)
	if e != nil {
		return nil, e
	}
	rt := newRuntime(c)
	if m.MintStarted != nil {
		if e := rt.vm.Set("fixtureMintStarted", m.MintStarted); e != nil {
			return nil, &Error{Stage: ErrRuntime, Code: "fixture-hook"}
		}
	}
	if e = rt.initialize(ctx, c, programs); e != nil {
		return nil, e
	}
	ch, found, e := rt.homepageChallenge(ctx, c.Page)
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
	}
	if !validChallenge(ch) {
		return nil, &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
	}
	interpreter, e := m.interpreter(ctx, c, ch)
	if e != nil {
		return nil, e
	}
	snapshot, e := rt.snapshot(ctx, ch, interpreter)
	if e != nil {
		return nil, e
	}
	issued := now() // Conservative TTL origin: request start, not response receipt.
	m.stats.integrity.Add(1)
	raw, e := m.request(ctx, c, "POST", rpcBase+"GenerateIT", []string{requestKey, snapshot}, ErrGenerateIT)
	if e != nil {
		return nil, e
	}
	token, ttl, threshold, e := parseIntegrity(raw)
	if e != nil {
		return nil, e
	}
	e = rt.minter(ctx, token, ttl.Seconds(), threshold)
	if e != nil {
		return nil, e
	}
	version, _ := interpreterID(ch)
	if e := ctx.Err(); e != nil {
		return nil, Failure(ErrMint, "initialization-canceled", e)
	}
	return &Session{rt: rt, Issued: issued, TTL: ttl, Version: version}, nil
}
func parseIntegrity(raw []byte) (string, time.Duration, float64, error) {
	var fields []json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) < 2 {
		return "", 0, 0, &Error{Stage: ErrIntegrity, Code: "invalid-integrity"}
	}
	var token string
	var seconds float64
	if json.Unmarshal(fields[0], &token) != nil || token == "" || json.Unmarshal(fields[1], &seconds) != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > float64(math.MaxInt64/int64(time.Second)) {
		return "", 0, 0, &Error{Stage: ErrIntegrity, Code: "invalid-integrity"}
	}
	var threshold float64
	if len(fields) > 2 {
		_ = json.Unmarshal(fields[2], &threshold)
	}
	// Upstream names field 2 mintRefreshThreshold but does not specify units.
	// Preserve it for BgUtils; do not invent a time/counter interpretation.
	if threshold < 0 || math.IsInf(threshold, 0) || math.IsNaN(threshold) {
		threshold = 0
	}
	return token, time.Duration(seconds * float64(time.Second)), threshold, nil
}
