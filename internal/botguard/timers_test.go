package botguard

import (
	"context"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/require"
)

func timerRuntime(t *testing.T) *runtime {
	t.Helper()
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	require.NoError(t, rt.initialize(context.Background(), fixtureContext(), ps))
	return rt
}

func TestTimerDOMSurface(t *testing.T) {
	rt := timerRuntime(t)
	probe, err := os.ReadFile("testdata/timer-surface-probe.js")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, rt.promise(ctx, `(`+string(probe)+`)`, "timerDone"))
	require.Empty(t, rt.timers, "timer cancellation/completion")
	require.Equal(t, "timer probe passed", rt.vm.Get("timerDone").String(), "timer cancellation/completion")
}

func TestIntervalConversionsAndBounds(t *testing.T) {
	rt := timerRuntime(t)
	require.NoError(t, rt.eval(`
 if ('pocSetInterval' in globalThis) throw Error('private host exposed');
 for (const delay of [-1, NaN, Infinity, 4294967297, 2147483648, 20000])
   setInterval(function(){}, delay);
 `))
	var delays []int64
	for _, timer := range rt.timers {
		require.True(t, timer.repeat, "interval registered as a timeout")
		delays = append(delays, timer.delay)
	}
	// Preserve the existing session's 10-second timer cap; delay wrapping is
	// browser conversion, the upper bound is an explicit extractor policy.
	require.Equal(t, []int64{0, 0, 0, 1, 0, 10000}, delays, "%v", delays)
}

func TestIntervalStringHandlerAndErrors(t *testing.T) {
	rt := timerRuntime(t)
	require.NoError(t, rt.eval(`var ticks=0;var id=setInterval('if(++ticks===2)clearInterval(id)',0);`))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, rt.pump(ctx, func() bool { return len(rt.timers) == 0 }))
	require.Equal(t, int64(2), rt.vm.Get("ticks").ToInteger(), "string handler did not execute in global scope")
	require.NoError(t, rt.eval(`setInterval(function(){throw new TypeError('timer test')},0);`))
	err := rt.pump(ctx, func() bool { return false })
	require.IsType(t, (*goja.Exception)(nil), err, "callback error must propagate through existing timer policy")
	require.Equal(t, 1, rt.timerErrors, "callback error must propagate through existing timer policy")
}

func TestIntervalRepeatedSelfCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rt := timerRuntime(t)
		require.NoError(t, rt.eval(`var ticks=0, id=setInterval(function(){if(++ticks===3)clearInterval(id)},5);`))
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, rt.pump(ctx, func() bool { return rt.vm.Get("ticks").ToInteger() == 3 }))
		require.Equal(t, 15*time.Millisecond, time.Since(start))
		require.Empty(t, rt.timers)
		require.Equal(t, 1, rt.nextID)
		require.Equal(t, 3, rt.timersFired)
	})
}
