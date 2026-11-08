package botguard

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSnapshotFailureStopsBeforeIntegrity(t *testing.T) {
	for _, tc := range []struct{ snapshot, code string }{
		{"", "empty-snapshot"}, {"E:fixture", "opaque-error-snapshot"}, {"synthetic-without-signal", "missing-minter-signal"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			engine, transport := newFake(t)
			transport.script = []byte(`var testGuard={a:function(program,setup){setup(function(cb){cb(` + strconv.Quote(tc.snapshot) + `)});return [function(){}]}};`)
			err := createFixture(engine, context.Background(), fixtureContext())
			require.ErrorIs(t, err, ErrSnapshot)
			var stage *Error
			require.ErrorAs(t, err, &stage)
			require.Equal(t, tc.code, stage.Code)
			require.Zero(t, transport.attestations.Load())
		})
	}
}

// Delayed setup used to be tested through recording/replay. Exercise the actual
// scheduler, BgUtils snapshot and mint callback without a historical transcript.
func TestDelayedBotGuardSetup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		engine, transport := newFake(t)
		script := strings.Replace(string(transport.script), " setup(function(callback,args){", " setTimeout(function(){setup(function(callback,args){", 1)
		script = strings.Replace(script, "},function(){},function(){},function(){});", "},function(){},function(){},function(){});},5);", 1)
		transport.script = []byte(script)
		handle, err := engine.Create(context.Background(), fixtureContext(), time.Now)
		require.NoError(t, err)
		defer handle.Close()
		require.Equal(t, 1, handle.rt.timersFired)
		token, err := handle.Mint(context.Background(), "video")
		require.NoError(t, err)
		require.NotEmpty(t, token)
		require.Equal(t, int64(1), transport.attestations.Load())
	})
}
