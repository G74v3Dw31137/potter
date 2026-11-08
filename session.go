package potter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/G74v3Dw31137/potter/internal/botguard"
)

type metrics struct{ ttl, lead, challenge, init, refresh, mints, hits, initTime, mintTime atomic.Int64 }

type tokenKey struct{ kind, binding string }
type cachedToken struct {
	value   string
	expires time.Time
}
type session struct {
	version            string
	clientVersion      string
	origin             botguard.Context
	key                string
	generation         uint64
	expires, refreshAt time.Time
	ttl, refreshLead   time.Duration
	users              int
	retired            bool
	rt                 *botguard.Session
	tokens             map[tokenKey]cachedToken
	dead               atomic.Bool
}
type build struct {
	done                 chan struct{}
	work                 context.Context
	cancel               context.CancelFunc
	key                  string
	revision, generation uint64 // immutable source acquisition opportunity
	err                  error
}
type proofs struct {
	Player, GVS string
	Generation  uint64
}

func (m *Client) tokens(ctx context.Context, c botguard.Context, video string) (SessionSnapshot, error) {
	if c.Binding != botguard.VideoBinding && c.Binding != botguard.VisitorBinding {
		return SessionSnapshot{}, ErrBinding
	}
	for range 2 {
		s, e := m.ensure(ctx, c)
		if e != nil {
			return SessionSnapshot{}, e
		}
		snapshot, e := func() (SessionSnapshot, error) {
			defer m.release(s)
			pair, err := m.sessionTokens(ctx, c, video, s)
			if err != nil {
				return SessionSnapshot{}, err
			}
			// Capture only immutable values from this successful lease. A newer
			// generation may already be current; it must never supply our origin.
			binding := video
			if s.origin.Binding == botguard.VisitorBinding {
				binding = s.origin.Visitor
			}
			return SessionSnapshot{
				tokens: Tokens{Player: pair.Player, GVS: pair.GVS, GVSBinding: binding,
					VisitorData: s.origin.Visitor, ClientVersion: s.clientVersion},
				generation: s.generation, documentURL: s.origin.DocumentURL, page: s.origin.Page,
			}, nil
		}()
		if e == errRetired && ctx.Err() == nil {
			continue
		}
		return snapshot, e
	}
	return SessionSnapshot{}, ErrMint
}

var errRetired = errors.New("retired BotGuard generation")

func (m *Client) sessionTokens(ctx context.Context, c botguard.Context, video string, s *session) (proofs, error) {
	if e := ctx.Err(); e != nil {
		return proofs{}, e
	}
	var e error
	if s.dead.Load() {
		return proofs{}, errRetired
	}
	if !m.now().Before(s.expires) {
		return proofs{}, ErrExpired
	}
	m.mu.Lock()
	superseded := m.desired != c.Key
	m.mu.Unlock()
	if superseded {
		return proofs{}, ErrIdentity
	}
	result := proofs{Generation: s.generation}
	binding := video
	if c.Binding == botguard.VisitorBinding {
		binding = c.Visitor
	}
	if video == "" || binding == "" {
		return proofs{}, ErrBinding
	}
	for i, k := range []tokenKey{{"player", video}, {"gvs", binding}} {
		now := m.now()
		if !now.Before(s.expires) {
			return proofs{}, ErrExpired
		}
		cached, ok := s.tokens[k]
		var value string
		if ok && now.Before(cached.expires) {
			value = cached.value
			m.stats.hits.Add(1)
		} else {
			start := time.Now()
			value, e = s.rt.Mint(ctx, k.binding)
			m.stats.mintTime.Add(int64(time.Since(start)))
			m.stats.mints.Add(1)
			if e != nil {
				s.dead.Store(true)
				m.failedMint(s, e)
				return proofs{}, e
			}
			if !m.now().Before(s.expires) {
				s.dead.Store(true)
				m.failedMint(s, ErrExpired)
				return proofs{}, ErrExpired
			}
			expires := m.now().Add(2 * time.Minute)
			if expires.After(s.expires) {
				expires = s.expires
			}
			if len(s.tokens) >= 256 {
				for key := range s.tokens {
					delete(s.tokens, key)
					break
				}
			}
			s.tokens[k] = cachedToken{value, expires}
		}
		if i == 0 {
			result.Player = value
		} else {
			result.GVS = value
		}
	}
	if ctx.Err() != nil {
		return proofs{}, ctx.Err()
	}
	m.mu.Lock()
	superseded = m.desired != c.Key
	m.mu.Unlock()
	if superseded {
		return proofs{}, ErrIdentity
	}
	return result, nil
}
func (m *Client) failedMint(s *session, e error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retireLocked(s) // Drop the failed generation's runtime/cache on lease release.
	if m.session == s {
		m.lastErr = e
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || errors.Is(e, ErrExpired) {
			m.retryAt = time.Time{}
		} else {
			m.retryAt = m.now().Add(5 * time.Second)
		}
	}
}
func (m *Client) ensure(ctx context.Context, c botguard.Context) (*session, error) {
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		if c.Revision < m.latest && m.desired != c.Key {
			m.mu.Unlock()
			return nil, ErrIdentity
		}
		m.observeLocked(c)
		now := m.now()
		s := m.session
		usable := s != nil && s.key == c.Key && !s.dead.Load() && now.Before(s.expires)
		if usable {
			if !now.Before(s.refreshAt) && m.job == nil && !now.Before(m.retryAt) {
				m.start(c)
			}
			s.users++
			m.mu.Unlock()
			return s, nil
		}
		if m.job != nil {
			job := m.job
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-job.done:
			}
			if job.err != nil && job.key == c.Key {
				return nil, m.buildError(c, job.err)
			}
			continue
		}
		if now.Before(m.retryAt) {
			e := m.lastErr
			if s != nil && s.key == c.Key && !now.Before(s.expires) {
				e = errors.Join(ErrExpired, e)
			}
			m.mu.Unlock()
			return nil, e
		}
		m.start(c)
		job := m.job
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-job.done:
		}
		if job.err != nil {
			return nil, m.buildError(c, job.err)
		}
	}
}
func (m *Client) start(c botguard.Context) {
	work, cancel := context.WithTimeout(m.life, 30*time.Second)
	job := &build{done: make(chan struct{}), work: work, cancel: cancel,
		key: c.Key, revision: c.Revision, generation: m.generation}
	m.job = job
	pageHash := sha256.Sum256(c.Page)
	pageKey := hex.EncodeToString(pageHash[:])
	bootstrap := len(c.Page) > 0 && m.lastPage != pageKey && m.now().Sub(c.CreatedAt) < 2*time.Minute
	if bootstrap {
		m.lastPage = pageKey
	}
	m.active.Go(func() {
		defer cancel()
		start := time.Now()
		s, e := m.factory(work, c, bootstrap)
		m.stats.initTime.Add(int64(time.Since(start)))
		m.stats.init.Add(1)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			e = ErrClosed
		} else if m.desired != c.Key {
			e = ErrIdentity
		} else if canceled := work.Err(); canceled != nil && e == nil {
			e = canceled
		}
		if e == nil {
			if s.origin.Revision < m.latest && s.origin.Key != m.desired {
				e = ErrIdentity
			}
			if e == nil {
				e = m.publishContextLocked(s.origin)
			}
		}
		if e == nil {
			m.desired = s.origin.Key
			if s.origin.Revision > m.latest {
				m.latest = s.origin.Revision
			}
			if m.generation > 0 {
				m.stats.refresh.Add(1)
			}
			m.generation++
			s.generation = m.generation
			s.clientVersion = m.current.version // parsed from this session's published origin
			s.key = s.origin.Key
			m.retireLocked(m.session)
			m.session = s
			m.stats.ttl.Store(int64(s.ttl))
			m.stats.lead.Store(int64(s.refreshLead))
			m.failures = 0
			m.lastErr = nil
			m.retryAt = time.Time{}
		} else if m.desired == c.Key {
			m.lastErr = e
			m.failures++
			step := min(m.failures, 6)
			delay := time.Duration(1<<step) * time.Second // 2..64s, bounded, plus <=25% jitter.
			m.retryAt = m.now().Add(delay + time.Duration(rand.Int64N(int64(delay/4)+1)))
		}
		if e != nil && s != nil {
			s.rt.Close()
		}
		job.err = e
		m.job = nil
		close(job.done)
	})
}
func (m *Client) buildError(c botguard.Context, e error) error {
	m.mu.Lock()
	s := m.session
	expired := s != nil && s.key == c.Key && !m.now().Before(s.expires)
	m.mu.Unlock()
	if expired {
		return errors.Join(ErrExpired, e)
	}
	return e
}

func (m *Client) observeLocked(c botguard.Context) {
	if c.Revision < m.latest {
		return
	}
	if m.desired != c.Key {
		m.desired = c.Key
		m.retireLocked(m.session)
		m.session = nil
		m.retryAt = time.Time{}
		m.lastErr = nil
		m.failures = 0
		if m.job != nil && m.job.key != c.Key {
			m.job.cancel()
		}
	}
	m.latest = c.Revision
	if s := m.session; s != nil && c.InterpreterVersion != "" && s.version != c.InterpreterVersion {
		s.refreshAt = m.now()
	}
}

// retireLocked never closes a handle while a public operation holds a lease.
// Cleanup touches no JS and does not wait. All fields are protected by Client.mu.
func (m *Client) retireLocked(s *session) {
	if s == nil {
		return
	}
	s.retired = true
	if s.users == 0 {
		s.rt.Close()
		s.tokens = nil
	}
}
func (m *Client) release(s *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.users--
	if s.retired && s.users == 0 {
		s.rt.Close()
		s.tokens = nil
	}
}

func (m *Client) createSession(ctx context.Context, c botguard.Context, bootstrap bool) (*session, error) {
	if !bootstrap {
		m.stats.challenge.Add(1)
		fresh, err := m.fetchContext(ctx, c.Visitor)
		if err != nil {
			return nil, err
		}
		c = fresh.guard
	}
	rt, err := m.engine.Create(ctx, c, m.now)
	if err != nil {
		return nil, err
	}
	expires := rt.Issued.Add(rt.TTL)
	margin := min(rt.TTL/10, time.Minute)
	if !m.now().Before(expires) {
		rt.Close()
		return nil, ErrExpired
	}
	return &session{version: rt.Version, origin: c, expires: expires, refreshAt: expires.Add(-margin), ttl: rt.TTL, refreshLead: margin, rt: rt, tokens: make(map[tokenKey]cachedToken)}, nil
}
