// Package httpboundary defines the three fixed production HTTP boundaries.
package httpboundary

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type Policy uint8

const (
	Homepage Policy = iota
	Interpreter
	Integrity
	maxRedirects = 5
)

var errBoundary = errors.New("potter: HTTP boundary rejected")

// AllowsURL accepts only exact delivery hosts, HTTPS and the default TLS port.
// Scheme-relative interpreter URLs must be resolved to HTTPS before this call.
func (p Policy) AllowsURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	authority := strings.ToLower(u.Host)
	if authority != host && authority != host+":443" {
		return false
	}
	switch p {
	case Homepage:
		return (host == "m.youtube.com" || host == "www.youtube.com") && u.EscapedPath() == "/"
	case Interpreter:
		return (host == "m.youtube.com" || host == "www.youtube.com" || host == "www.google.com") && u.Path != "" && u.Path != "/"
	case Integrity:
		return host == "jnn-pa.googleapis.com" && u.EscapedPath() == "/$rpc/google.internal.waa.v1.Waa/GenerateIT" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
	default:
		return false
	}
}

func (p Policy) allowsRequest(r *http.Request) bool {
	if !p.AllowsURL(r.URL) || (r.Host != "" && !strings.EqualFold(r.Host, r.URL.Host)) {
		return false
	}
	if p == Integrity {
		return r.Method == http.MethodPost
	}
	return r.Method == http.MethodGet && (r.Body == nil || r.Body == http.NoBody) && r.ContentLength <= 0 && len(r.TransferEncoding) == 0
}

func stripRedirectHeaders(r *http.Request) {
	// net/http's default rules do not recognize Potter's custom identity/RPC
	// headers. Remove them on every redirected GET, including same-host hops.
	for key := range r.Header {
		switch strings.ToLower(key) {
		case "x-goog-visitor-id", "x-goog-api-key", "x-user-agent", "referer":
			delete(r.Header, key)
		}
	}
}

type transport struct {
	base   http.RoundTripper
	policy Policy
}

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	// This runs after CheckRedirect and cookie-jar processing, immediately
	// before the caller's transport. A callback cannot bypass URL/method rules.
	if !t.policy.allowsRequest(r) {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, errBoundary
	}
	if err := r.Context().Err(); err != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, err
	}
	return t.base.RoundTrip(r)
}

// Do borrows hc's transport and jar. Only the per-request client copy changes;
// timeout, cancellation and caller ownership are preserved. Call sites retain
// their existing stage-specific redaction of all HTTP errors.
func (p Policy) Do(hc *http.Client, req *http.Request) (*http.Response, error) {
	if !p.allowsRequest(req) {
		return nil, errBoundary
	}
	client := *hc
	base := hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = transport{base: base, policy: p}
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if p == Integrity || len(via) > maxRedirects || !p.allowsRequest(next) {
			return errBoundary
		}
		stripRedirectHeaders(next)
		if hc.CheckRedirect != nil {
			if err := hc.CheckRedirect(next, via); err != nil {
				return err
			}
		}
		if !p.allowsRequest(next) {
			return errBoundary
		}
		stripRedirectHeaders(next)
		return next.Context().Err()
	}
	return client.Do(req)
}
