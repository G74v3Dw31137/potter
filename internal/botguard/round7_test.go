package botguard

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRound7DOMSurface(t *testing.T) {
	m, _ := newFake(t)
	ps, err := m.staticPrograms(context.Background())
	require.NoError(t, err)
	rt := newRuntime(fixtureContext())
	err = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, err)
	probe, err := os.ReadFile("testdata/round7-surface-probe.js")
	require.NoError(t, err)
	_, err = rt.vm.RunString(string(probe))
	require.NoError(t, err)
}
