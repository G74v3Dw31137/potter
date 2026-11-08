package botguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"

	"github.com/G74v3Dw31137/potter/internal/httpboundary"
	"github.com/dop251/goja"
)

// DescribeInterpreter parses the homepage's loose JS data in a tiny, bounded VM.
// It never loads BgUtils, DOM, the interpreter, or makes a network request.
func DescribeInterpreter(ctx context.Context, page []byte) (string, error) {
	rt := &runtime{vm: goja.New()}
	ch, found, e := rt.homepageChallenge(ctx, page)
	if e != nil {
		return "", e
	}
	if !found || !validChallenge(ch) {
		return "", &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
	}
	return interpreterID(ch)
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
