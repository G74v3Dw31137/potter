package potter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/G74v3Dw31137/potter/internal/botguard"
	"github.com/stretchr/testify/require"
)

const validConfig = `{"INNERTUBE_CONTEXT":{"client":{"clientName":"MWEB","clientVersion":"fixture-version","visitorData":"PRIVATE-visitor"}},"WEB_PLAYER_CONTEXT_CONFIGS":{"WEB_PLAYER_CONTEXT_CONFIG_ID_MWEB_WATCH":{"serializedExperimentFlags":"html5_generate_content_po_token=true"}}}`
const validChallenge = `window.ytAtN({R:{bgChallenge:{program:'fixture-program',globalName:'testGuard',interpreterUrl:{privateDoNotAccessOrElseTrustedResourceUrlWrappedValue:'https://www.google.com/botguard/fixture.js'}}}});`

func TestHomepageLoggedIn(t *testing.T) {
	for _, tc := range []struct {
		name, field, code string
		loggedIn          bool
	}{
		{"true", `"LOGGED_IN":true,`, "logged-in", true},
		{"false", `"LOGGED_IN":false,`, "", false},
		{"missing", "", "", false},
		{"whitespace", "\"LOGGED_IN\": \n false\t,", "", false},
		{"empty", `"LOGGED_IN":,`, "invalid-homepage-config", false},
		{"empty-string", `"LOGGED_IN":"",`, "invalid-logged-in", false},
		{"string", `"LOGGED_IN":"false",`, "invalid-logged-in", false},
		{"number", `"LOGGED_IN":0,`, "invalid-logged-in", false},
		{"null", `"LOGGED_IN":null,`, "invalid-logged-in", false},
		{"array", `"LOGGED_IN":[],`, "invalid-logged-in", false},
		{"object", `"LOGGED_IN":{},`, "invalid-logged-in", false},
		{"duplicate-invalid", `"LOGGED_IN":null,"LOGGED_IN":false,`, "invalid-logged-in", false},
		{"syntax", `"LOGGED_IN":fals,`, "invalid-homepage-config", false},
		{"suffix", `"LOGGED_IN":false+1,`, "invalid-homepage-config", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := "{" + tc.field + validConfig[1:]
			page := []byte("<script>ytcfg.set(" + cfg + ");" + validChallenge + "</script>")
			parsed, err := parsePage(context.Background(), page, 1)
			if tc.code == "" {
				require.NoError(t, err)
				require.Equal(t, botguard.VideoBinding, parsed.guard.Binding)
				var normalized map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(parsed.guard.Config, &normalized))
				_, present := normalized["LOGGED_IN"]
				require.Equal(t, tc.field != "", present, "absence must remain distinct from explicit false")
			} else {
				require.ErrorIs(t, err, ErrChallenge)
				var classified *Error
				require.ErrorAs(t, err, &classified)
				require.Equal(t, tc.code, classified.Code)
				require.NotContains(t, err.Error(), "PRIVATE")
			}
			if tc.field == "" || tc.name == "true" || tc.name == "false" {
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(cfg), &fields))
				value, err := parseLoggedIn(fields)
				require.NoError(t, err)
				require.Equal(t, tc.loggedIn, value)
			}
		})
	}
}

func TestHomepageExtraction(t *testing.T) {
	for _, tc := range []struct {
		name, config, challenge string
		bad                     bool
	}{
		{"existing-object", "ytcfg.set(" + validConfig + ");", validChallenge, false},
		{"surrounding-html", "ytcfg.set(" + validConfig + ");</script><script>", validChallenge, false},
		{"spacing-comments", "ytcfg.set /* call */ (\n" + validConfig + " /* end */ ) ;", validChallenge, false},
		{"multiple-calls", "ytcfg.set(" + validConfig + `);ytcfg.set({"LOGGED_IN":false});`, validChallenge, false},
		{"first-call-remains-authoritative", "ytcfg.set(" + validConfig + `);ytcfg.set({"INNERTUBE_CONTEXT":null});`, validChallenge, false},
		{"later-invalid-flag", "ytcfg.set(" + validConfig + `);ytcfg.set({"LOGGED_IN":"false"});`, validChallenge, true},
		{"overwritten-invalid-flag", `ytcfg.set({"LOGGED_IN":"false"});ytcfg.set({"LOGGED_IN":false});ytcfg.set(` + validConfig + ");", validChallenge, true},
		{"nested-delimiter-string", "ytcfg.set(" + strings.Replace(validConfig, "{", `{"extra":{"nested":["}); PRIVATE \\\"",{}]},`, 1) + ");", validChallenge, false},
		{"challenge-string", "ytcfg.set(" + validConfig + ");", `window.ytAtN({R:'{"bgChallenge":{"program":"fixture-program","globalName":"testGuard","interpreterJavascript":{"privateDoNotAccessOrElseSafeScriptWrappedValue":"var x=1;"}}}'});`, false},
		{"nested-challenge", "ytcfg.set(" + validConfig + ");", strings.Replace(validChallenge, "program:'fixture-program'", "program:(function(){/* }) */ return 'fixture-program })';})()", 1), false},
		{"missing-config", "", validChallenge, true},
		{"commented-config", "/* ytcfg.set(" + validConfig + "); */", validChallenge, true},
		{"quoted-config", `'ytcfg.set({})';`, validChallenge, true},
		{"null-visitor", "ytcfg.set(" + strings.Replace(validConfig, "{", `{"VISITOR_DATA":null,`, 1) + ");", validChallenge, true},
		{"extra-argument", "ytcfg.set(" + validConfig + ", 1);", validChallenge, true},
		{"unbalanced", "ytcfg.set(" + validConfig[:len(validConfig)-1] + ");", validChallenge, true},
		{"trailing-expression", "ytcfg.set(" + validConfig + "+{});", validChallenge, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePage(context.Background(), []byte("<script>"+tc.config+tc.challenge+"</script>"), 1)
			if tc.bad {
				require.ErrorIs(t, err, ErrChallenge)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHomepageNonObjectCalls(t *testing.T) {
	for _, tc := range []struct{ name, extra string }{
		{"empty-challenge", `window.ytAtN();`},
		{"forwarded-challenge", `window.ytAtN(payload);`},
		{"string-challenge", `window.ytAtN("event");`},
		{"empty-config", `ytcfg.set();`},
		{"key-value-config", `ytcfg.set("EXTRA",true);`},
		{"forwarded-config", `ytcfg.set(extraConfig);`},
	} {
		for _, position := range []string{"before", "after"} {
			t.Run(tc.name+"/"+position, func(t *testing.T) {
				page := "ytcfg.set(" + validConfig + ");" + validChallenge
				if position == "before" {
					page = tc.extra + page
				} else {
					page += tc.extra
				}
				parsed, err := parsePage(context.Background(), []byte("<script>"+page+"</script>"), 1)
				require.NoError(t, err)
				require.Equal(t, "fixture-version", parsed.version)
				require.Equal(t, botguard.VideoBinding, parsed.guard.Binding)
				require.NotEmpty(t, parsed.guard.InterpreterVersion)
			})
		}
	}
}

func TestHomepageNonObjectCallsDoNotHideInvalidFlags(t *testing.T) {
	for _, tc := range []struct{ value, code string }{
		{`"false"`, "invalid-logged-in"},
		{`null`, "invalid-logged-in"},
		{`0`, "invalid-logged-in"},
		{``, "invalid-homepage-config"},
		{`true`, "logged-in"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			cfg := `{"LOGGED_IN":` + tc.value + "," + validConfig[1:]
			page := `window.ytAtN();ytcfg.set("EXTRA",true);ytcfg.set(` + cfg + ");" + validChallenge
			_, err := parsePage(context.Background(), []byte("<script>"+page+"</script>"), 1)
			require.ErrorIs(t, err, ErrChallenge)
			var classified *Error
			require.ErrorAs(t, err, &classified)
			require.Equal(t, tc.code, classified.Code)
		})
	}
}

func TestHomepageChallengeAfterScriptLineComment(t *testing.T) {
	for _, end := range []string{"</script>", "</SCRIPT>", "</script >", "\n</script>", "\r</script>"} {
		t.Run(end, func(t *testing.T) {
			// HTML ends the script even when its final JS line comment has no
			// newline. That comment must not hide a subsequent script's data.
			page := "<script>ytcfg.set(" + validConfig + ");</script>" +
				"<script>window.ytAtN();// bootstrap" + end +
				"<script>" + validChallenge + "</script>"
			parsed, err := parsePage(context.Background(), []byte(page), 1)
			require.NoError(t, err)
			require.Equal(t, "fixture-version", parsed.version)
			require.Equal(t, botguard.VideoBinding, parsed.guard.Binding)
			require.NotEmpty(t, parsed.guard.InterpreterVersion)
		})
	}
}

func TestHomepageLineCommentWithSimilarTag(t *testing.T) {
	page := "<script>ytcfg.set(" + validConfig + ");</script>" +
		"<script>// ignored </scripture>window.ytAtN({R:{}});\n" + validChallenge + "</script>"
	_, err := parsePage(context.Background(), []byte(page), 1)
	require.NoError(t, err, "a similar tag name must not expose the commented-out payload")
}
