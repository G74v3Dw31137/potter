package potter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/G74v3Dw31137/potter/internal/botguard"
	"github.com/G74v3Dw31137/potter/internal/httpboundary"
)

const homepage = "https://m.youtube.com/"

type pageContext struct {
	guard   botguard.Context
	version string
}

// parsePage retains the extractor's MWEB client normalization and binding rules.
// Player JS URL parsing is excluded: it serves only the deciphering subsystem.
func parsePage(ctx context.Context, page []byte, revision uint64) (pageContext, error) {
	cfg, err := homepageConfig(page)
	if err != nil {
		return pageContext{}, err
	}
	loggedIn, err := parseLoggedIn(cfg)
	if err != nil {
		return pageContext{}, err
	}
	if loggedIn {
		return pageContext{}, &Error{Stage: ErrChallenge, Code: "logged-in"}
	}
	var inner map[string]json.RawMessage
	if json.Unmarshal(cfg["INNERTUBE_CONTEXT"], &inner) != nil {
		return pageContext{}, &Error{Stage: ErrChallenge, Code: "invalid-client-config"}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(inner["client"], &fields) != nil {
		return pageContext{}, &Error{Stage: ErrChallenge, Code: "invalid-client-config"}
	}
	str := func(key string) string { var value string; _ = json.Unmarshal(fields[key], &value); return value }
	visitor, version := str("visitorData"), str("clientVersion")
	if str("clientName") != "MWEB" || version == "" || visitor == "" {
		return pageContext{}, &Error{Stage: ErrChallenge, Code: "invalid-client-config"}
	}
	ua, _ := json.Marshal(UserAgent)
	fields["userAgent"] = ua
	client, _ := json.Marshal(fields)
	if raw, ok := cfg["VISITOR_DATA"]; ok {
		var top string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &top) != nil || top != "" && top != visitor {
			return pageContext{}, &Error{Stage: ErrChallenge, Code: "invalid-visitor-data"}
		}
	}
	var configs map[string]struct {
		Flags string `json:"serializedExperimentFlags"`
	}
	if json.Unmarshal(cfg["WEB_PLAYER_CONTEXT_CONFIGS"], &configs) != nil {
		return pageContext{}, &Error{Stage: ErrBinding, Code: "invalid-watch-config"}
	}
	watch, ok := configs["WEB_PLAYER_CONTEXT_CONFIG_ID_MWEB_WATCH"]
	if !ok {
		return pageContext{}, &Error{Stage: ErrBinding, Code: "invalid-watch-config"}
	}
	flags, err := url.ParseQuery(watch.Flags)
	if err != nil {
		return pageContext{}, &Error{Stage: ErrBinding, Code: "invalid-watch-config"}
	}
	binding := botguard.VisitorBinding
	values := flags["html5_generate_content_po_token"]
	if len(values) > 0 {
		switch values[len(values)-1] {
		case "true":
			binding = botguard.VideoBinding
		case "false":
		default:
			return pageContext{}, &Error{Stage: ErrBinding, Code: "invalid-watch-config"}
		}
	}
	inner["client"] = client
	raw, _ := json.Marshal(inner)
	cfg["INNERTUBE_CONTEXT"] = raw
	raw, _ = json.Marshal(cfg)
	interpreter, err := botguard.DescribeInterpreter(ctx, page)
	if err != nil {
		return pageContext{}, err
	}
	hash := sha256.Sum256(append(append([]byte{}, client...), byte(binding)))
	return pageContext{version: version, guard: botguard.Context{
		InterpreterVersion: interpreter, Key: hex.EncodeToString(hash[:]), Revision: revision,
		CreatedAt: time.Now(), Page: page, Config: raw, Visitor: visitor, UserAgent: UserAgent, Binding: binding,
	}}, nil
}

func (c *Client) fetchContext(ctx context.Context, visitor string) (pageContext, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, homepage, nil)
	req.Header.Set("User-Agent", UserAgent)
	if visitor != "" {
		req.Header.Set("X-Goog-Visitor-Id", visitor)
	}
	resp, err := httpboundary.Homepage.Do(c.http, req)
	if err != nil {
		return pageContext{}, botguard.HTTPFailure(ctx, ErrChallenge, false, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return pageContext{}, &Error{Stage: ErrChallenge, Code: "http-status", StatusCode: resp.StatusCode}
	}
	// The boundary supplies missing transport metadata from the actual final
	// dispatched request, including redirects. Never guess from the initial URL.
	if resp.Request == nil || !httpboundary.Homepage.AllowsURL(resp.Request.URL) {
		return pageContext{}, &Error{Stage: ErrChallenge, Code: "invalid-document-url"}
	}
	documentURL := resp.Request.URL.String()
	page, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return pageContext{}, botguard.HTTPFailure(ctx, ErrChallenge, true, err)
	}
	if len(page) > 8<<20 {
		return pageContext{}, &Error{Stage: ErrChallenge, Code: "response-too-large"}
	}
	c.mu.Lock()
	c.revision++
	revision := c.revision
	c.mu.Unlock()
	input, err := c.parse(ctx, page, revision)
	if err != nil {
		return pageContext{}, err
	}
	input.guard.DocumentURL = documentURL
	return input, nil
}

// Caller holds Client.mu, making context and generation publication atomic.
func (c *Client) publishContextLocked(input botguard.Context) error {
	if c.closed {
		return ErrClosed
	}
	if c.current.guard.Revision > input.Revision {
		return ErrIdentity
	}
	var cfg struct {
		Context struct {
			Client struct {
				Version string `json:"clientVersion"`
			}
		} `json:"INNERTUBE_CONTEXT"`
	}
	if json.Unmarshal(input.Config, &cfg) != nil {
		return &Error{Stage: ErrChallenge, Code: "invalid-client-config"}
	}
	c.current = pageContext{guard: input, version: cfg.Context.Client.Version}
	c.contextExpires = time.Now().Add(30 * time.Minute)
	return nil
}

func (c *Client) context(ctx context.Context) (pageContext, error) {
	var input pageContext
	var expired bool
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return pageContext{}, ErrClosed
		}
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return pageContext{}, err
		}
		input = c.current
		expired = !time.Now().Before(c.contextExpires)
		// Foreground calls hold gate, but a background builder does not. Join it
		// before issuing another homepage request. The source revision identifies
		// immutable page/configuration storage; key includes the normalized client
		// configuration and binding. A replaced generation or canceled builder is
		// a different opportunity. Client HTTP policy is fixed at construction.
		job := c.job
		if !expired || input.guard.Key == "" || job == nil ||
			job.key != input.guard.Key || c.desired != job.key ||
			job.revision != input.guard.Revision || job.generation != c.generation || job.work.Err() != nil {
			c.mu.Unlock()
			break
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-job.done:
		}
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return pageContext{}, ErrClosed
		}
		if err := ctx.Err(); err != nil {
			return pageContext{}, err
		}
		if errors.Is(job.err, ErrIdentity) {
			continue // a replacement observed during the wait owns the next attempt
		}
		if job.err != nil {
			return pageContext{}, c.buildError(input.guard, job.err)
		}
		// Re-read the atomically published context and session. Never publish a
		// shared builder's homepage independently of its validated minter.
	}
	if input.guard.Key == "" || expired {
		var err error
		input, err = c.fetchContext(ctx, "")
		if err != nil {
			return pageContext{}, err
		}
		c.mu.Lock()
		if err = ctx.Err(); err == nil || c.closed {
			err = c.publishContextLocked(input.guard)
			if err == ErrIdentity {
				// A background builder published a newer complete generation while
				// this fetch was parsing. Adopt its context under the same lock.
				input, err = c.current, nil
			}
		}
		c.mu.Unlock()
		if err != nil {
			return pageContext{}, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return pageContext{}, ErrClosed
	}
	c.observeLocked(input.guard)
	return input, nil
}

// homepageConfig keeps the first configuration call authoritative, as before.
// Later calls must not hide malformed identity flags, but do not change the
// successful client normalization or binding chosen from the first call.
func homepageConfig(page []byte) (map[string]json.RawMessage, error) {
	var cfg map[string]json.RawMessage
	var semanticErr error
	err := botguard.VisitPageObjects(page, "ytcfg.set", func(object []byte) bool {
		fields, err := decodeHomepageConfig(object)
		if err != nil {
			semanticErr = err
			return false
		}
		if cfg == nil {
			cfg = fields
		}
		return true
	})
	if err != nil {
		return nil, &Error{Stage: ErrChallenge, Code: "invalid-homepage-config"}
	}
	if semanticErr != nil {
		return nil, semanticErr
	}
	if cfg == nil {
		return nil, &Error{Stage: ErrChallenge, Code: "missing-homepage-config"}
	}
	return cfg, nil
}

func decodeHomepageConfig(object []byte) (map[string]json.RawMessage, error) {
	// Decode fields individually so a duplicate key cannot overwrite a malformed
	// LOGGED_IN value before validation. Other keys retain JSON's last-value rule.
	decoder := json.NewDecoder(bytes.NewReader(object))
	if _, err := decoder.Token(); err != nil { // The recognizer already requires '{'.
		return nil, &Error{Stage: ErrChallenge, Code: "invalid-homepage-config"}
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, &Error{Stage: ErrChallenge, Code: "invalid-homepage-config"}
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nil, &Error{Stage: ErrChallenge, Code: "invalid-homepage-config"}
		}
		name, ok := key.(string)
		if !ok {
			return nil, &Error{Stage: ErrChallenge, Code: "invalid-homepage-config"}
		}
		fields[name] = raw
		if name == "LOGGED_IN" {
			if _, err := parseLoggedIn(fields); err != nil {
				return nil, err
			}
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, &Error{Stage: ErrChallenge, Code: "invalid-homepage-config"}
	}
	return fields, nil
}

// Absence remains accepted: existing anonymous homepage fixtures omit the flag.
// JSON null is not a boolean (json.Unmarshal into bool alone would accept it).
func parseLoggedIn(cfg map[string]json.RawMessage) (bool, error) {
	raw, present := cfg["LOGGED_IN"]
	if !present {
		return false, nil
	}
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, &Error{Stage: ErrChallenge, Code: "invalid-logged-in"}
	}
}
