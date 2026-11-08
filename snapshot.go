package potter

import (
	"bytes"
	"context"
	"fmt"
	"io"
)

// SessionSnapshot is an immutable result containing a PO Token pair and the
// homepage artifact associated with its originating minter. Its zero value is
// safe to inspect and contains empty tokens, generation, URL and body.
//
// A snapshot remains readable after session replacement or Client.Close. It
// retains the acquired homepage storage (bounded to 8 MiB of body payload per
// acquisition), but no client, runtime or lease. Keeping snapshots from many
// acquisitions can retain unbounded total memory. It does not guarantee continued
// remote validity of its tokens. Keep tokens and homepage contents private.
type SessionSnapshot struct {
	tokens      Tokens
	generation  uint64
	documentURL string
	page        []byte // shares immutable acquisition storage; never exported
}

// TokensWithSession acquires a token pair and its originating session artifact
// atomically. It follows the same acquisition, caching, cancellation and error
// semantics as Client.Tokens, and returns a zero snapshot on failure. Pass a
// non-nil context. Exposing the artifact adds no HTTP requests or JS execution.
func (c *Client) TokensWithSession(ctx context.Context, videoID string) (SessionSnapshot, error) {
	return c.acquireTokens(ctx, videoID)
}

// Tokens returns a value copy of the existing public token fields.
func (s SessionSnapshot) Tokens() Tokens { return s.tokens }

// Generation identifies the originating minter generation. It is meaningful only
// within the originating Client, not a remote session ID or freshness guarantee.
func (s SessionSnapshot) Generation() uint64 { return s.generation }

// Homepage returns the final validated homepage document URL and a new independent
// reader positioned at offset zero. The reader performs no network operations,
// requires no Close, and shares read-only storage with the snapshot. Use separate
// readers for concurrent reading. The body contains the original acquired response
// bytes (as delivered by the HTTP client), not normalized runtime configuration.
func (s SessionSnapshot) Homepage() (documentURL string, body io.Reader) {
	return s.documentURL, bytes.NewReader(s.page)
}

// Format redacts proofs, identity and homepage contents from diagnostic output.
func (s SessionSnapshot) Format(state fmt.State, verb rune) {
	_, _ = io.WriteString(state, "potter.SessionSnapshot{redacted}")
}
