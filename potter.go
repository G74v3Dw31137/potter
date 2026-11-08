// Package potter generates anonymous YouTube Player and GVS PO tokens using
// the original BotGuard callback in an embedded Goja and LinkeDOM environment.
// Its API is experimental. Minting does not establish server acceptance.
package potter

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/G74v3Dw31137/potter/internal/botguard"
)

// UserAgent is the MWEB browser identity used by the extracted implementation.
const UserAgent = "Mozilla/5.0 (iPad; CPU OS 16_7_10 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1"

// Error identifies a failed protocol stage and, when available, HTTP status.
// It omits raw responses, proofs and JavaScript exception details.
type Error = botguard.Error

var (
	ErrChallenge   = botguard.ErrChallenge
	ErrInterpreter = botguard.ErrInterpreter
	ErrRuntime     = botguard.ErrRuntime
	ErrSnapshot    = botguard.ErrSnapshot
	ErrGenerateIT  = botguard.ErrGenerateIT
	ErrIntegrity   = botguard.ErrIntegrity
	ErrMint        = botguard.ErrMint
	ErrExpired     = botguard.ErrExpired
	ErrIdentity    = botguard.ErrIdentity
	ErrBinding     = botguard.ErrBinding
	ErrClosed      = botguard.ErrClosed
)

// Tokens contains separate proofs, minted in Player-then-GVS order. Keep these
// values and the anonymous identity private. Do not log this struct.
type Tokens struct {
	// Player is bound to the requested video ID for serviceIntegrityDimensions.poToken.
	Player string
	// GVS is for a conventional media URL's pot parameter.
	GVS string
	// GVSBinding is the actual video ID or visitor data selected by MWEB WATCH flags.
	GVSBinding string
	// VisitorData and ClientVersion identify the MWEB context used for both proofs.
	VisitorData, ClientVersion string
}

// Option configures a client before use.
type Option func(*config)

type config struct{ http *http.Client }

// WithHTTPClient uses a copy of hc's policy and shares its transport. The transport
// must honor request contexts. Use an anonymous client without credentials/cookies.
// Close never closes a caller-owned transport.
func WithHTTPClient(hc *http.Client) Option { return func(c *config) { c.http = hc } }

// Client coordinates one managed BotGuard session. Construct it with NewClient
// and close it after use. Public operations are serialized and may be called
// concurrently. No pool is created. The zero value is not usable.
type Client struct {
	// mu protects lifecycle metadata; gate serializes public work. active accounts
	// both admitted callers (including queued ones) and the single shared builder.
	http           *http.Client
	engine         *botguard.Engine
	life           context.Context
	cancel         context.CancelFunc
	gate           chan struct{}
	mu             sync.Mutex
	closed         bool
	active         sync.WaitGroup
	closeOnce      sync.Once
	current        pageContext
	contextExpires time.Time
	revision       uint64
	latest         uint64
	desired        string
	session        *session
	job            *build
	retryAt        time.Time
	failures       int
	lastErr        error
	lastPage       string
	generation     uint64
	stats          metrics
	now            func() time.Time
	factory        func(context.Context, botguard.Context, bool) (*session, error) // offline test seam
	parse          func(context.Context, []byte, uint64) (pageContext, error)      // offline publication barrier
}

// NewClient allocates a client without network I/O. Initialization is lazy;
// Warmup can explicitly load the homepage, interpreter and integrity session.
func NewClient(options ...Option) (*Client, error) {
	cfg := config{http: &http.Client{Timeout: 30 * time.Second}}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	if cfg.http == nil {
		return nil, errors.New("potter: nil HTTP client")
	}
	hc := *cfg.http
	life, cancel := context.WithCancel(context.Background())
	c := &Client{http: &hc, life: life, cancel: cancel, gate: make(chan struct{}, 1)}
	c.engine = botguard.NewEngine(c.http)
	c.now = time.Now
	c.factory = c.createSession
	c.parse = parsePage
	return c, nil
}

func (c *Client) begin(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, ErrClosed
	}
	c.active.Add(1)
	c.mu.Unlock()
	work, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.life, cancel)
	done := func() { stop(); cancel(); c.active.Done() }
	select {
	case c.gate <- struct{}{}:
		return work, func() { <-c.gate; done() }, nil
	case <-work.Done():
		done()
		return nil, nil, work.Err()
	}
}

// Warmup initializes the same session used by Tokens. Cancellation stops waiting;
// shared initialization can continue under its context budget until Close.
// Compilation and native Go work may delay build cancellation and Close.
func (c *Client) Warmup(ctx context.Context) error {
	ctx, done, err := c.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	for range 2 {
		input, err := c.context(ctx)
		if err != nil {
			return err
		}
		var s *session
		s, err = c.ensure(ctx, input.guard)
		if s != nil {
			c.release(s)
		}
		if !errors.Is(err, ErrIdentity) {
			return err
		}
	}
	return ErrIdentity
}

// Tokens generates both proofs for videoID using the original mint callback.
// Player always binds to videoID; GVS binds according to the homepage flags.
// No Player API call or media request is made. An empty videoID fails locally.
func (c *Client) Tokens(ctx context.Context, videoID string) (Tokens, error) {
	ctx, done, err := c.begin(ctx)
	if err != nil {
		return Tokens{}, err
	}
	defer done()
	if videoID == "" {
		return Tokens{}, &Error{Stage: ErrBinding, Code: "empty-video-id"}
	}
	for range 2 {
		input, err := c.context(ctx)
		if err != nil {
			return Tokens{}, err
		}
		proofs, err := c.tokens(ctx, input.guard, videoID)
		if errors.Is(err, ErrIdentity) {
			continue
		}
		if err != nil {
			return Tokens{}, err
		}
		binding := videoID
		if input.guard.Binding == botguard.VisitorBinding {
			binding = input.guard.Visitor
		}
		return Tokens{Player: proofs.Player, GVS: proofs.GVS, GVSBinding: binding,
			VisitorData: input.guard.Visitor, ClientVersion: input.version}, nil
	}
	return Tokens{}, ErrIdentity
}

// Close cancels owned work, joins workers and releases runtime references.
// It is safe to repeat or call concurrently with public operations.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		c.mu.Unlock()
		c.active.Wait()
		c.mu.Lock()
		c.retireLocked(c.session)
		c.session = nil
		c.engine.Close()
		c.current = pageContext{}
		c.contextExpires = time.Time{}
		c.mu.Unlock()
	})
	return nil
}
