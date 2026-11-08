package botguard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPageScriptIsolation(t *testing.T) {
	const real = `ytcfg.set({"real":true});`
	const fake = `ytcfg.set({"fake":true});`
	for _, tc := range []struct {
		name, page string
		want       []string
		bad        bool
	}{
		{"apostrophe-in-text", `<p>Don't</p><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"paragraph-call", `<p>` + fake + `</p><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"attribute-call-and-quoted-angle", `<p title='>` + fake + `'>text</p><script data-x='>'>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"html-comment", `<!-- <script>` + fake + `</script> --><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"bare-js-is-not-html-script", real, nil, false},
		{"json", `<script type=application/json>` + fake + `</script><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"plain", `<script type=text/plain>` + fake + `</script><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"module-conservatively-excluded", `<script type=module>` + fake + `</script><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"src", `<script src=x>` + fake + `</script><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"empty-src", `<script src>` + fake + `</script><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"nomodule", `<script nomodule>` + fake + `</script><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"template", `<template><script>` + fake + `</script></template><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"nested-template", `<template><template></template><script>` + fake + `</script></template><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"self-closing-template-is-still-inert", `<template/><script>` + fake + `</script></template><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"foreign-container", `<svg><script>` + fake + `</script></svg><math><script>` + fake + `</script></math><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"noscript", `<noscript><script>` + fake + `</script></noscript><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"textarea", `<textarea><script>` + fake + `</script></textarea><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"style", `<style><script>` + fake + `</script></style><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"iframe", `<iframe><script>` + fake + `</script></iframe><script>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"plaintext", `<plaintext><script>` + fake + `</script>`, nil, false},
		{"case-and-type-space", `<ScRiPt TYPE=' Text/JavaScript '>` + real + `</sCrIpT >`, []string{`{"real":true}`}, false},
		{"empty-type-overrides-language", `<script type='' language=unknown>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"legacy-language", `<script language=JavaScript>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"unknown-language", `<script language=vbscript>` + fake + `</script>`, nil, false},
		{"duplicate-type-first-inert", `<script type=text/plain type=text/javascript>` + fake + `</script>`, nil, false},
		{"duplicate-type-first-js", `<script type=text/javascript type=text/plain>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"mime-parameters-excluded", `<script type='text/javascript;charset=utf-8'>` + fake + `</script>`, nil, false},
		{"document-order", `<script>` + fake + `</script><script>` + real + `</script>`, []string{`{"fake":true}`, `{"real":true}`}, false},
		{"truncated-eligible-script-excluded", `<script>` + real, nil, false},
		{"truncated-inert-script", `<script type=text/plain>` + fake, nil, false},
		{"truncated-opening-tag-no-fallback", `<script title='` + fake, nil, false},
		{"unclosed-template-stays-inert", `<template><script>` + fake + `</script>`, nil, false},
		{"self-closing-script-slash-is-ignored", `<script/>` + real + `</script>`, []string{`{"real":true}`}, false},
		{"script-double-escaped-state", `<script><!--<script>` + fake + `</script>-->
` + real + `</script>`, []string{`{"real":true}`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			err := VisitPageObjects([]byte(tc.page), "ytcfg.set", func(b []byte) bool { got = append(got, string(b)); return true })
			if tc.bad {
				require.ErrorIs(t, err, ErrChallenge)
				require.NotContains(t, err.Error(), "fake")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, got, "only eligible script source contributes, in document order")
		})
	}
}

func TestPageScriptBoundaries(t *testing.T) {
	for _, broken := range []string{"//", "/*", "'", `"`, "`"} {
		for _, end := range []string{"</script>", "</SCRIPT>", "</script >", "</script/>", "</script\f>"} {
			t.Run(broken+end, func(t *testing.T) {
				page := []byte("<script>" + broken + end + `<script>ytcfg.set({"later":true});</script>`)
				count := 0
				err := VisitPageObjects(page, "ytcfg.set", func(b []byte) bool { count++; require.Equal(t, `{"later":true}`, string(b)); return true })
				require.NoError(t, err)
				require.Equal(t, 1, count, "malformed earlier script cannot hide later data")
			})
		}
	}
	// Similar names do not terminate raw script text or its JS line comment.
	var got []string
	require.NoError(t, VisitPageObjects([]byte("<script>// </scripture>ytcfg.set({});\nyt cfg;ytcfg.set({\"real\":true});</script>"), "ytcfg.set", func(b []byte) bool { got = append(got, string(b)); return true }))
	require.Equal(t, []string{`{"real":true}`}, got)
}

func TestPageSourceBytesPreserved(t *testing.T) {
	object := "{\r\n\"data\":\"&amp;\\\\\\\"\",\"nul\":\"\x00\"}"
	count := 0
	require.NoError(t, VisitPageObjects([]byte("<script>ytcfg.set("+object+")</script>"), "ytcfg.set", func(b []byte) bool {
		count++
		require.Equal(t, object, string(b), "no entity decoding, NUL replacement or newline normalization")
		return true
	}))
	require.Equal(t, 1, count, "the original payload must actually be discovered")
}

func TestScriptCandidateLexing(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         []string
	}{
		{"regex-quotes", `const r=/"/;ytcfg.set({});`, []string{`{}`}},
		{"regex-braces", `ytcfg.set({x:/}/});`, []string{`{x:/}/}`}},
		{"regex-call-text", `const r=/[ytcfg.set({})]/;`, nil},
		{"regex-escapes-and-class", `const r=/["/{}]\\\//gi;ytcfg.set({});`, []string{`{}`}},
		{"regex-return", `function f(){return /"/;}ytcfg.set({});`, []string{`{}`}},
		{"regex-control-parentheses", `if (ok) /[ytcfg.set({})]/.test(x);ytcfg.set({});`, []string{`{}`}},
		{"division", `const n=a/b/c;ytcfg.set({});`, []string{`{}`}},
		{"division-after-call", `f() / 2;ytcfg.set({});`, []string{`{}`}},
		{"nested-inert-template", "const s = `${`ytcfg.set({})`}`;", nil},
		{"template-interpolation", "const s = `${ytcfg.set({})}`;", []string{`{}`}},
		{"nested-template-interpolation", "const s = `${`text ${ytcfg.set({})}`}`;", []string{`{}`}},
		{"template-object-interpolation", "const s = `${({x:ytcfg.set({})})}`;", []string{`{}`}},
		{"template-inert-text", "const s = `ytcfg.set({})`;", nil},
		{"template-escaped-interpolation", "const s = `\\${ytcfg.set({})}`;", nil},
		{"template-escaped-backtick", "const s = `\\` ytcfg.set({})`;", nil},
		{"template-in-payload", "ytcfg.set({s:`x${{a:`${1}`}.a}`});", []string{"{s:`x${{a:`${1}`}.a}`}"}},
		{"quotes-and-backslashes", `const a='\\\'ytcfg.set({})';const b="\\\"ytcfg.set({})";ytcfg.set({"x":"});\\\""});`, []string{`{"x":"});\\\""}`}},
		{"legacy-open-line-comment", "<!-- ignored ytcfg.set({})\nyt cfg;ytcfg.set({});", []string{`{}`}},
		{"legacy-close-line-comment", "  --> ytcfg.set({})\nyt cfg;ytcfg.set({});", []string{`{}`}},
		{"legacy-close-not-at-line-start", `x --> ytcfg.set({});`, []string{`{}`}},
		{"comment-in-call", `ytcfg.set /*x*/ ( /*y*/ {} /*z*/ )`, []string{`{}`}},
		{"non-object", `window.ytAtN();ytcfg.set("KEY",value);ytcfg.set(foo);ytcfg.set({});ytcfg.set();`, []string{`{}`}},
		{"identifier-prefix", `myytcfg.set({});obj.ytcfg.set({});$ytcfg.set({});ytcfg.setter({});`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			_, err := scanPageScript([]byte(tc.source), "ytcfg.set", func(b []byte) bool { got = append(got, string(b)); return true })
			require.NoError(t, err)
			require.Equal(t, tc.want, got, "discover executable interpolation; ignore literal text and preserve payloads")
		})
	}
	for _, space := range []string{" ", "\t", "\n", "\r", "\v", "\f", "\u00a0", "\u1680", "\u2000", "\u202f", "\u205f", "\u3000", "\ufeff", "\u2028", "\u2029"} {
		t.Run("space-"+space, func(t *testing.T) {
			count := 0
			_, err := scanPageScript([]byte("ytcfg.set"+space+"("+space+"{}"+space+")"), "ytcfg.set", func([]byte) bool { count++; return true })
			require.NoError(t, err)
			require.Equal(t, 1, count, "ECMAScript whitespace is allowed between call tokens")
		})
	}
	for _, line := range []string{"\n", "\r", "\r\n", "\u2028", "\u2029"} {
		t.Run("line-"+line, func(t *testing.T) {
			count := 0
			_, err := scanPageScript([]byte("//ytcfg.set({})"+line+"ytcfg.set({});"), "ytcfg.set", func([]byte) bool { count++; return true })
			require.NoError(t, err)
			require.Equal(t, 1, count, "all JS line terminators end line comments")
		})
	}
}

func TestPageExtractionBounds(t *testing.T) {
	for _, source := range []string{
		"ytcfg.set({" + strings.Repeat("[", maxPageNesting) + "0" + strings.Repeat("]", maxPageNesting) + "})",
		strings.Repeat("(", maxPageNesting+1),
		strings.Repeat("`a${", maxPageNesting+1),
		"ytcfg.set({PRIVATE:])", "ytcfg.set({PRIVATE:1}", "ytcfg.set({PRIVATE:1},0)",
	} {
		_, err := scanPageScript([]byte(source), "ytcfg.set", func([]byte) bool { return true })
		require.ErrorIs(t, err, ErrChallenge)
		require.NotContains(t, err.Error(), "PRIVATE")
	}
	require.ErrorIs(t, VisitPageObjects([]byte(strings.Repeat("x", maxPageBytes+1)), "ytcfg.set", func([]byte) bool { return true }), ErrChallenge)
	// Long literals and many candidates are linear scans without candidate storage.
	count := 0
	page := "<script>const s='" + strings.Repeat("x", maxPageBytes-100) + "';ytcfg.set({});</script>"
	require.NoError(t, VisitPageObjects([]byte(page), "ytcfg.set", func([]byte) bool { count++; return true }))
	require.Equal(t, 1, count)
	count = 0
	require.NoError(t, VisitPageObjects([]byte("<script>"+strings.Repeat("ytcfg.set({});", 20000)+"</script>"), "ytcfg.set", func([]byte) bool { count++; return true }))
	require.Equal(t, 20000, count)
	require.ErrorIs(t, VisitPageObjects([]byte(strings.Repeat("<template>", maxPageNesting+1)), "ytcfg.set", func([]byte) bool { return true }), ErrChallenge)
}
