package botguard

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/G74v3Dw31137/potter/internal/httpboundary"
)

const requestKey = "O43z0dpjhgX20SCx4KAo"
const rpcBase = "https://jnn-pa.googleapis.com/$rpc/google.internal.waa.v1.Waa/"

func (m *Engine) request(ctx context.Context, c Context, method, target string, payload any, stage error) ([]byte, error) {
	var policy httpboundary.Policy
	switch stage {
	case ErrChallenge:
		policy = httpboundary.Homepage
	case ErrInterpreter:
		policy = httpboundary.Interpreter
	case ErrGenerateIT:
		policy = httpboundary.Integrity
	default:
		return nil, &Error{Stage: stage, Code: "invalid-request"}
	}
	// Check the original authority before NewRequest canonicalizes it (notably
	// dropping an empty port). No headers or payload have been attached yet.
	u, e := url.Parse(target)
	if e != nil || !policy.AllowsURL(u) {
		return nil, &Error{Stage: stage, Code: "invalid-request"}
	}
	var body io.Reader
	if payload != nil {
		data, e := json.Marshal(payload)
		if e != nil {
			return nil, &Error{Stage: stage, Code: "invalid-request"}
		}
		body = bytes.NewReader(data)
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return nil, &Error{Stage: stage, Code: "invalid-request"}
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("X-Goog-Visitor-Id", c.Visitor)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json+protobuf")
		// Public Google API key used by the upstream BgUtils BotGuard client.
		// Source: https://github.com/LuanRT/BgUtils/blob/main/src/utils/constants.ts
		req.Header.Set("x-goog-api-key", "AIzaSyDyT5W0Jh49F30Pqqtyfdf7pDLFKLJoAnw")
		req.Header.Set("x-user-agent", "grpc-web-javascript/0.1")
	}
	resp, e := policy.Do(m.http, req)
	if e != nil {
		return nil, HTTPFailure(ctx, stage, false, e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &Error{Stage: stage, Code: "http-status", StatusCode: resp.StatusCode}
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if e != nil {
		return nil, HTTPFailure(ctx, stage, true, e)
	}
	if len(data) > 8<<20 {
		return nil, &Error{Stage: stage, Code: "response-too-large"}
	}
	return data, nil
}
