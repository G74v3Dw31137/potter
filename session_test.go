package potter

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter/internal/botguard"
	"github.com/stretchr/testify/require"
)

// Only these coordinator-policy tests replace construction. Public lifecycle
// tests exercise the actual protocol and runtime via the offline transport.
func coordinatorFixture(t *testing.T) (*Client, botguard.Context) {
	t.Helper()
	c, err := NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.Close()) })
	input := botguard.Context{Key: "identity", Revision: 1, Config: []byte(`{"INNERTUBE_CONTEXT":{"client":{"clientVersion":"test"}}}`)}
	c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
		return &session{origin: input, version: input.InterpreterVersion, rt: &botguard.Session{}, tokens: make(map[tokenKey]cachedToken), expires: c.now().Add(time.Minute), refreshAt: c.now().Add(54 * time.Second)}, nil
	}
	return c, input
}
func ensureAndRelease(t *testing.T, c *Client, input botguard.Context) *session {
	t.Helper()
	s, err := c.ensure(context.Background(), input)
	require.NoError(t, err)
	c.release(s)
	return s
}

func TestCoordinatorBackoffBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, input := coordinatorFixture(t)
		attempts := 0
		failure := errors.New("controlled initialization failure")
		c.factory = func(context.Context, botguard.Context, bool) (*session, error) { attempts++; return nil, failure }
		for i := 1; i <= 8; i++ {
			_, err := c.ensure(context.Background(), input)
			require.ErrorIs(t, err, failure)
			minimum := time.Duration(1<<min(i, 6)) * time.Second
			c.mu.Lock()
			delay := c.retryAt.Sub(c.now())
			c.mu.Unlock()
			require.GreaterOrEqual(t, delay, minimum)
			require.LessOrEqual(t, delay, minimum+minimum/4)
			for range 3 {
				_, err = c.ensure(context.Background(), input)
				require.ErrorIs(t, err, failure)
			}
			require.Equal(t, i, attempts)
			time.Sleep(minimum + minimum/4)
		}
		require.NoError(t, c.Close())
	})
}

func TestCoordinatorRetirementWaitsForLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, input := coordinatorFixture(t)
		old := ensureAndRelease(t, c, input)
		build := c.factory
		entered, finish := make(chan struct{}), make(chan struct{})
		c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
			close(entered)
			<-finish
			return build(ctx, input, bootstrap)
		}
		time.Sleep(54 * time.Second)
		leased, err := c.ensure(context.Background(), input)
		require.NoError(t, err)
		require.Same(t, old, leased)
		<-entered
		close(finish)
		synctest.Wait()
		c.mu.Lock()
		require.NotSame(t, old, c.session)
		require.True(t, old.retired)
		require.Equal(t, 1, old.users)
		require.NotNil(t, old.tokens)
		require.Equal(t, c.current.guard.Key, c.session.origin.Key)
		c.mu.Unlock()
		c.release(old)
		c.mu.Lock()
		require.Zero(t, old.users)
		require.Nil(t, old.tokens)
		c.mu.Unlock()
		require.NoError(t, c.Close())
	})
}

func TestCoordinatorRejectsCompletedBuildAfterClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, input := coordinatorFixture(t)
		build := c.factory
		entered := make(chan struct{})
		c.factory = func(ctx context.Context, input botguard.Context, bootstrap bool) (*session, error) {
			close(entered)
			<-ctx.Done()
			return build(ctx, input, bootstrap)
		}
		result := make(chan error, 1)
		go func() { _, err := c.ensure(context.Background(), input); result <- err }()
		<-entered
		require.NoError(t, c.Close())
		require.ErrorIs(t, <-result, ErrClosed)
		c.mu.Lock()
		defer c.mu.Unlock()
		require.Nil(t, c.session)
		require.Nil(t, c.job)
		require.Empty(t, c.current.guard.Key)
		require.Zero(t, c.generation)
	})
}

func TestCoordinatorIdentityAndInterpreterObservation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, input := coordinatorFixture(t)
		input.InterpreterVersion = "first"
		old := ensureAndRelease(t, c, input)
		next := input
		next.Revision++
		next.InterpreterVersion = "second"
		c.mu.Lock()
		c.observeLocked(next)
		require.Equal(t, c.now(), old.refreshAt)
		c.mu.Unlock()
		ensureAndRelease(t, c, next)
		synctest.Wait()
		c.mu.Lock()
		require.Equal(t, "second", c.session.version)
		c.mu.Unlock()
		// A stale interpreter hint cannot roll back a published generation.
		c.mu.Lock()
		c.observeLocked(input)
		require.Equal(t, "second", c.session.version)
		c.mu.Unlock()
		next.Key = "replacement"
		next.Revision++
		c.mu.Lock()
		c.observeLocked(next)
		require.Nil(t, c.session)
		c.mu.Unlock()
		_, err := c.ensure(context.Background(), input)
		require.ErrorIs(t, err, ErrIdentity)
		ensureAndRelease(t, c, next)
		require.NoError(t, c.Close())
	})
}

func TestCoordinatorFailureIdentityDuringRefresh(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, input := coordinatorFixture(t)
				old := ensureAndRelease(t, c, input)
				failure := botguard.Failure(ErrGenerateIT, "deadline", cause)
				c.factory = func(context.Context, botguard.Context, bool) (*session, error) { return nil, failure }
				time.Sleep(54 * time.Second)
				require.Same(t, old, ensureAndRelease(t, c, input))
				synctest.Wait()
				// A failed refresh leaves the live generation usable, then retains
				// both the build cause and expiry when it can no longer be used.
				time.Sleep(6 * time.Second)
				_, err := c.ensure(context.Background(), input)
				require.ErrorIs(t, err, ErrExpired)
				require.ErrorIs(t, err, ErrGenerateIT)
				require.ErrorIs(t, err, cause)
			})
		})
	}
}

func TestCoordinatorMintFailureRetainsReason(t *testing.T) {
	c, input := coordinatorFixture(t)
	s := ensureAndRelease(t, c, input)
	failure := &Error{Stage: ErrMint, Code: "empty-token"}
	c.failedMint(s, failure)
	c.mu.Lock()
	defer c.mu.Unlock()
	require.Same(t, failure, c.lastErr)
	require.WithinDuration(t, c.now().Add(5*time.Second), c.retryAt, time.Second)
}
