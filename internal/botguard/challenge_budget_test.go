package botguard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/require"
)

// Count the actual fallback VM allocation and evaluation, rather than inferring
// execution from interpreter HTTP requests (whose compiled program is cached).
func TestChallengeDescriptionBudget(t *testing.T) {
	base := string(mockPage(1))
	for _, tc := range []struct {
		name, page string
		vms        int
	}{
		{"json-object", base, 0},
		{"loose-literal", strings.ReplaceAll(base, `"R":`, `R:`), 0},
		{"comment", strings.Replace(base, `"R":`, `/*data*/R:`, 1), 0},
		{"string-response", stringResponsePage(t, base), 0},
		{"primitive-metadata", strings.Replace(base, `"R":`, `extra:{n:12,b:true,z:null},"R":`, 1), 0},
		{"escaped-names", strings.Replace(base, `"R":`, `"\u0052":`, 1), 0},
		{"escaped-value", strings.Replace(base, `"fixture-program"`, `'fixture-\u0070rogram'`, 1), 0},
		{"inline-interpreter", strings.Replace(base, `"interpreterUrl":{"privateDoNotAccessOrElseTrustedResourceUrlWrappedValue":"https://www.google.com/botguard/interpreter-v1.js"}`, `interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'var testGuard={};'}`, 1), 0},
		{"function", strings.Replace(base, `"fixture-program"`, `(function(){return 'fixture-program'})()`, 1), 1},
		{"getter", strings.Replace(base, `"program":"fixture-program"`, `get program(){return 'fixture-program'}`, 1), 1},
		{"computed-key", strings.Replace(base, `"R":`, `['R']:`, 1), 1},
		{"spread", strings.Replace(base, `"R":`, `...{},"R":`, 1), 1},
		{"prototype", strings.Replace(base, `"R":`, `__proto__:null,"R":`, 1), 1},
		{"duplicate", strings.Replace(base, `"R":`, `x:'first',x:'last',"R":`, 1), 1},
		{"array-metadata", strings.Replace(base, `"R":`, `extra:[],"R":`, 1), 1},
		{"regexp-metadata", strings.Replace(base, `"R":`, `extra:/}/,"R":`, 1), 1},
		{"template", strings.Replace(base, `"fixture-program"`, "`fixture-program`", 1), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := []byte(tc.page)
			var oldStats metrics
			legacy := &runtime{vm: goja.New(), stats: &oldStats}
			ch, found, err := legacy.homepageChallenge(context.Background(), page)
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, int64(1), oldStats.challenges.Load(), "previous discovery always evaluated the data")
			want, err := interpreterID(ch)
			require.NoError(t, err)
			var stats metrics
			allocations := 0
			got, err := describeInterpreter(context.Background(), page, func() *runtime {
				allocations++
				return &runtime{vm: goja.New(), stats: &stats}
			})
			require.NoError(t, err)
			require.Equal(t, want, got)
			require.Equal(t, tc.vms, allocations)
			require.Equal(t, int64(tc.vms), stats.challenges.Load())
			require.Zero(t, stats.initializations.Load())
			require.Zero(t, stats.executions.Load())
			require.Zero(t, stats.integrity.Load())
			if parsed, ok := literalChallenge(mustChallengeObject(t, page)); ok {
				require.Equal(t, ch, parsed, "all challenge fields must match, not just interpreter identity")
			}
		})
	}
}

func mustChallengeObject(t *testing.T, page []byte) []byte {
	t.Helper()
	object, err := homepageChallengeObject(page)
	require.NoError(t, err)
	return object
}

func stringResponsePage(t *testing.T, page string) string {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(mustChallengeObject(t, []byte(page)), &payload))
	raw, err := json.Marshal(payload["R"])
	require.NoError(t, err)
	payload["R"] = string(raw)
	raw, err = json.Marshal(payload)
	require.NoError(t, err)
	return "<script>window.ytAtN(" + string(raw) + ")</script>"
}

func TestChallengeDescriptionErrorsAndCancellation(t *testing.T) {
	for _, replacement := range []string{
		`{R:'PRIVATE invalid JSON'}`, `{R:null}`, `{R:{}}`, `{R:{bgChallenge:{}}}`,
		`{R:{bgChallenge:{get program(){throw Error('PRIVATE')}}}}`,
		`{R:{bgChallenge:undefined}}`, `{R:{bgChallenge:`,
	} {
		t.Run(replacement, func(t *testing.T) {
			page := []byte("<script>window.ytAtN(" + replacement + ")</script>")
			_, err := DescribeInterpreter(context.Background(), page)
			legacy := &runtime{vm: goja.New()}
			ch, found, oldErr := legacy.homepageChallenge(context.Background(), page)
			if oldErr == nil && (!found || !validChallenge(ch)) {
				oldErr = &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
			}
			require.EqualError(t, err, oldErr.Error())
			require.NotContains(t, err.Error(), "PRIVATE")
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := describeInterpreter(ctx, mockPage(1), func() *runtime {
		t.Fatal("canceled discovery allocated a VM")
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, ErrChallenge)
}

func TestChallengeSessionStateAndBudget(t *testing.T) {
	m, f := newFake(t)
	// An interpreter may observe attPayload, its prototypes, and normalization
	// of a string R. Discovery must not replace session-side data evaluation.
	f.script = append([]byte(`if(!attPayload.R.bgChallenge || Object.getPrototypeOf(attPayload)!==Object.prototype)throw Error('payload');`), f.script...)
	c := fixtureContext()
	c.Page = []byte(stringResponsePage(t, string(c.Page)))
	for i := int64(1); i <= 2; i++ {
		_, err := DescribeInterpreter(context.Background(), c.Page)
		require.NoError(t, err)
		s, err := m.Create(context.Background(), c, time.Now)
		require.NoError(t, err)
		_, err = s.Mint(context.Background(), "player")
		require.NoError(t, err)
		_, err = s.Mint(context.Background(), "gvs")
		require.NoError(t, err)
		s.Close()
		require.Equal(t, Stats{InterpreterRequests: 1, GenerateITRequests: i,
			RuntimeInitializations: i, ChallengeEvaluations: i, InterpreterExecutions: i, MintExecutions: 2 * i}, m.Stats())
	}
	require.Equal(t, int64(1), f.gets.Load())
	require.Equal(t, int64(2), f.attestations.Load())
}
