package botguard

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func foundationProbe(t *testing.T) []byte {
	t.Helper()
	m, _ := newFake(t)
	ps, e := m.staticPrograms(context.Background())
	require.NoError(t, e)
	rt := newRuntime(fixtureContext())
	e = rt.initialize(context.Background(), fixtureContext(), ps)
	require.NoError(t, e)
	b, e := os.ReadFile("testdata/foundation-probe.js")
	require.NoError(t, e)
	v, e := rt.vm.RunString(string(b))
	require.NoError(t, e)
	return []byte(v.String())
}

// Each leaf has two exact observations and a manifest contract. No xfail filters.
type differentialCase struct {
	Contract, Classification string
	Current, Reference       any
}

func flattenProbe(b []byte) (map[string]any, error) {
	var v map[string]map[string]any
	if e := json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	out := map[string]any{}
	for section, values := range v {
		for key, value := range values {
			if section == "surface" {
				for member, d := range value.(map[string]any) {
					out[section+"."+key+"."+member] = d
				}
			} else {
				out[section+"."+key] = value
			}
		}
	}
	return out, nil
}
func TestFoundationDifferential(t *testing.T) {
	b, e := os.ReadFile("testdata/foundation-expectations.json")
	require.NoError(t, e)
	var cases map[string]differentialCase
	e = json.Unmarshal(b, &cases)
	require.NoError(t, e)
	current, e := flattenProbe(foundationProbe(t))
	require.NoError(t, e)
	var manifest struct {
		Entries []struct {
			Interface string
			Members   []string
		}
	}
	mb, _ := os.ReadFile("compatibility-manifest.json")
	e = json.Unmarshal(mb, &manifest)
	require.NoError(t, e)
	contracts := map[string]bool{}
	for _, entry := range manifest.Entries {
		for _, m := range entry.Members {
			contracts[entry.Interface+"."+m] = true
		}
	}
	check := func(t *testing.T, actual map[string]any, reference bool) {
		t.Helper()
		require.Len(t, actual, len(cases), "case count changed")
		for k, c := range cases {
			t.Run(k, func(t *testing.T) {
				want := c.Current
				if reference {
					want = c.Reference
				}
				v, ok := actual[k]
				require.True(t, ok, "%s changed; review contract %s\n got %#v\nwant %#v", k, c.Contract, v, want)
				assert.Equal(t, want, v, "%s changed; review contract %s", k, c.Contract)
			})
		}
	}
	for k, c := range cases {
		require.True(t, contracts[c.Contract], "%s: unknown contract %s", k, c.Contract)
		same := reflect.DeepEqual(c.Current, c.Reference)
		if same {
			require.Equal(t, "agreement", c.Classification, "%s: invalid classification", k)
		} else {
			require.Contains(t, []string{"known-defect", "reference-disagreement"}, c.Classification, "%s: invalid classification", k)
		}
	}
	t.Run("goja", func(t *testing.T) { check(t, current, false) })

}
