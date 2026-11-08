package potter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireHomepageCode(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		require.NoError(t, err)
		return
	}
	require.ErrorIs(t, err, ErrChallenge)
	var classified *Error
	require.ErrorAs(t, err, &classified)
	require.Equal(t, code, classified.Code)
	require.NotContains(t, err.Error(), "PRIVATE")
}

func TestHomepageChallengePrecedence(t *testing.T) {
	firstPage := []byte("<script>ytcfg.set(" + validConfig + ");" + validChallenge + "</script>")
	first, err := parsePage(context.Background(), firstPage, 1)
	require.NoError(t, err)
	different := strings.Replace(validChallenge, "fixture.js", "second.js", 1)
	for _, tc := range []struct{ name, source, code string }{
		{"valid-then-truncated", validChallenge + `window.ytAtN({);`, ""},
		{"valid-then-extra-argument", validChallenge + `window.ytAtN({},0);`, ""},
		{"valid-then-truncated-next-script", validChallenge + `</script><script>window.ytAtN({);`, ""},
		{"valid-then-unclosed-script", validChallenge + `</script><script>window.ytAtN({);`, ""},
		{"truncated-first-is-authoritative", `window.ytAtN({);` + validChallenge, "invalid-homepage-data"},
		{"truncated-first-next-script", `window.ytAtN({);</script><script>` + validChallenge, "invalid-homepage-data"},
		{"invalid-first-semantic", `window.ytAtN({R:{bgChallenge:{}}});` + validChallenge, "invalid-challenge"},
		{"two-valid-first-wins", validChallenge + different, ""},
		{"nonobjects-surround", `window.ytAtN();window.ytAtN(foo);` + validChallenge + `window.ytAtN("event");`, ""},
		{"regex-in-payload", strings.Replace(validChallenge, "R:", "extra:/}/,R:", 1), ""},
		{"template-in-payload", strings.Replace(validChallenge, "R:", "extra:`${{x:`${1}`}.x}`,R:", 1), ""},
		{"challenge-in-interpolation", "const s=`${" + validChallenge[:len(validChallenge)-1] + "}`;", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := "<script>ytcfg.set(" + validConfig + ");" + tc.source + "</script>"
			// An unclosed suffix script cannot erase a challenge already selected.
			if tc.name == "valid-then-unclosed-script" {
				page = strings.TrimSuffix(page, "</script>")
			}
			got, err := parsePage(context.Background(), []byte(page), 1)
			requireHomepageCode(t, err, tc.code)
			if tc.code == "" {
				require.Equal(t, first.guard.InterpreterVersion, got.guard.InterpreterVersion, "first eligible object-bearing challenge wins")
			}
		})
	}
}

func TestHomepageConfigAuthority(t *testing.T) {
	good := "ytcfg.set(" + validConfig + ");"
	for _, tc := range []struct{ name, source, code string }{
		{"later-invalid-type", good + `ytcfg.set({"LOGGED_IN":null});`, "invalid-logged-in"},
		{"later-invalid-syntax", good + `ytcfg.set({"LOGGED_IN":fals});`, "invalid-homepage-config"},
		{"later-truncated", good + `ytcfg.set({);`, "invalid-homepage-config"},
		{"first-truncated", `ytcfg.set({);</script><script>` + good, "invalid-homepage-config"},
		{"first-invalid-json", `ytcfg.set({PRIVATE invalid});` + good, "invalid-homepage-config"},
		{"first-incomplete-config", `ytcfg.set({});` + good, "invalid-client-config"},
		{"html-cannot-conceal-first-invalid", `</script><p>'</p><script>ytcfg.set({"LOGGED_IN":null});</script><p>'</p><script>` + good, "invalid-logged-in"},
		{"later-true-does-not-merge", good + `ytcfg.set({"LOGGED_IN":true});`, ""},
		{"later-valid-config-does-not-replace", good + "ytcfg.set(" + strings.Replace(validConfig, "fixture-version", "second", 1) + ");", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parsePage(context.Background(), []byte("<script>"+tc.source+validChallenge+"</script>"), 1)
			requireHomepageCode(t, err, tc.code)
			if tc.code == "" {
				require.Equal(t, "fixture-version", parsed.version, "first eligible configuration remains authoritative")
			}
		})
	}
}

func TestHomepageValidBooleanDuplicates(t *testing.T) {
	for _, tc := range []struct{ fields, code, last string }{
		{`"LOGGED_IN":true,"LOGGED_IN":false,`, "", "false"},
		{`"LOGGED_IN":false,"LOGGED_IN":true,`, "logged-in", "true"},
		{`"LOGGED_IN":false,"LOGGED_IN":false,`, "", "false"},
	} {
		t.Run(tc.fields, func(t *testing.T) {
			cfg := "{" + tc.fields + validConfig[1:]
			page := []byte("<script>ytcfg.set(" + cfg + ");" + validChallenge + "</script>")
			parsed, err := parsePage(context.Background(), page, 1)
			requireHomepageCode(t, err, tc.code)
			if tc.code == "" {
				var normalized map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(parsed.guard.Config, &normalized))
				require.Equal(t, tc.last, string(normalized["LOGGED_IN"]), "valid duplicate booleans retain last-value behavior")
			}
		})
	}
}

func TestHomepageHTMLContextAndBoundaries(t *testing.T) {
	real := "<script>ytcfg.set(" + validConfig + ");" + validChallenge + "</script>"
	fake := `ytcfg.set({"LOGGED_IN":null});window.ytAtN({R:{}});`
	for _, prefix := range []string{
		"<p>Don't</p>", "<p>" + fake + "</p>", "<div title='>" + fake + "'></div>",
		"<!--<script>" + fake + "</script>-->", "<template><script>" + fake + "</script></template>",
		"<script type=application/json>" + fake + "</script>", "<script type=text/plain>" + fake + "</script>",
		"<script src='x'>" + fake + "</script>", "<noscript><script>" + fake + "</script></noscript>",
		"<script>//</SCRIPT>", "<script>/*</script>", "<script>'</script>", "<script>\"</script>", "<script>`</script>",
	} {
		t.Run(prefix, func(t *testing.T) {
			parsed, err := parsePage(context.Background(), []byte(prefix+real), 1)
			require.NoError(t, err, "HTML and earlier broken scripts cannot contaminate either data call")
			require.Equal(t, "fixture-version", parsed.version)
		})
	}
	_, err := parsePage(context.Background(), []byte("ytcfg.set("+validConfig+");"+validChallenge), 1)
	requireHomepageCode(t, err, "missing-homepage-config")
}
