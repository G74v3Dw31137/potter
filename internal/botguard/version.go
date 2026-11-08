package botguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"

	"github.com/G74v3Dw31137/potter/internal/httpboundary"
	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// DescribeInterpreter reads literal challenge data without executing JavaScript.
// Other expressions retain the tiny, bounded VM path. Neither path loads BgUtils,
// DOM, the interpreter, or makes a network request. Session-side evaluation is
// deliberately independent: it establishes attPayload in the initialized VM.
func DescribeInterpreter(ctx context.Context, page []byte) (string, error) {
	return describeInterpreter(ctx, page, func() *runtime { return &runtime{vm: goja.New()} })
}

func describeInterpreter(ctx context.Context, page []byte, newVM func() *runtime) (string, error) {
	object, e := homepageChallengeObject(page)
	if e != nil {
		return "", e
	}
	if object == nil {
		return "", &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
	}
	if e := ctx.Err(); e != nil {
		return "", Failure(ErrChallenge, "canceled", e)
	}
	ch, literal := literalChallenge(object)
	if e := ctx.Err(); e != nil {
		return "", Failure(ErrChallenge, "execution-canceled", e)
	}
	if !literal {
		ch, e = newVM().evaluateChallenge(ctx, object)
		if e != nil {
			return "", e
		}
	}
	if !validChallenge(ch) {
		return "", &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
	}
	return interpreterID(ch)
}

// literalChallenge accepts only data with no observable evaluation effects.
// Unsupported or malformed data falls back to Goja, preserving its validation,
// exception classification and loose-JavaScript semantics. Nothing is cached.
func literalChallenge(object []byte) (challenge, bool) {
	var ch challenge
	if object == nil {
		return ch, false
	}
	p, e := parser.ParseFile(nil, "", "("+string(object)+")", 0)
	if e != nil || len(p.Body) != 1 {
		return ch, false
	}
	s, ok := p.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return ch, false
	}
	value, ok := literalData(s.Expression)
	payload, isObject := value.(map[string]any)
	if !ok || !isObject {
		return ch, false
	}
	r := payload["R"]
	if text, ok := r.(string); ok {
		if json.Unmarshal([]byte(text), &r) != nil {
			return ch, false
		}
	}
	response, ok := r.(map[string]any)
	if !ok {
		return ch, false
	}
	value, exists := response["bgChallenge"]
	if !exists {
		return ch, false
	}
	raw, e := json.Marshal(value)
	if e != nil || json.Unmarshal(raw, &ch) != nil {
		return challenge{}, false
	}
	return ch, true
}

func literalData(expr ast.Expression) (any, bool) {
	switch v := expr.(type) {
	case *ast.StringLiteral:
		return v.Value.String(), true
	case *ast.BooleanLiteral:
		return v.Value, true
	case *ast.NullLiteral:
		return nil, true
	case *ast.NumberLiteral:
		// BigInt and other non-JSON values require Goja's export semantics.
		switch n := v.Value.(type) {
		case int64:
			// The parser retains exact int64 values, but Goja rounds integers
			// outside Number's safe range to float64. Keep those on its path.
			if n < -(1<<53-1) || n > 1<<53-1 {
				return nil, false
			}
			return n, true
		case float64:
			return n, true
		}
	case *ast.ObjectLiteral:
		data := make(map[string]any, len(v.Value))
		for _, property := range v.Value {
			p, ok := property.(*ast.PropertyKeyed)
			if !ok || p.Computed || p.Kind != ast.PropertyKindValue {
				return nil, false
			}
			key, ok := p.Key.(*ast.StringLiteral)
			// Goja normalizes unquoted property names into string literals.
			if !ok || key.Value.String() == "__proto__" {
				return nil, false
			}
			name := key.Value.String()
			// Keep duplicate-property semantics on the original Goja path.
			if _, duplicate := data[name]; duplicate {
				return nil, false
			}
			value, ok := literalData(p.Value)
			if !ok {
				return nil, false
			}
			data[name] = value
		}
		return data, true
	}
	return nil, false
}
func interpreterID(ch challenge) (string, error) {
	if source := ch.InterpreterJavascript.Value; source != "" {
		h := sha256.Sum256([]byte(source))
		return "inline:" + hex.EncodeToString(h[:]) + "#" + ch.InterpreterHash, nil
	}
	u, e := url.Parse(ch.InterpreterUrl.Value)
	if e != nil {
		return "", &Error{Stage: ErrInterpreter, Code: "invalid-interpreter-url"}
	}
	if u.Scheme == "" && u.Host != "" {
		u.Scheme = "https"
	}
	if !httpboundary.Interpreter.AllowsURL(u) {
		return "", &Error{Stage: ErrInterpreter, Code: "invalid-interpreter-url"}
	}
	return u.String() + "#" + ch.InterpreterHash, nil
}
