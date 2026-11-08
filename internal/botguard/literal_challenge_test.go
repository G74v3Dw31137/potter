package botguard

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/stretchr/testify/require"
)

const literalFixture = `{program:'program',globalName:'guard',interpreterHash:'hash',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'source'}}`

// Exercise extraction, all challenge fields, validation, interpreter identity,
// and the actual fallback allocation. The reference always evaluates in Goja.
func checkLiteralChallenge(t *testing.T, object string, fast bool) (challenge, error) {
	t.Helper()
	ctx := context.Background()
	page := []byte("<script>window.ytAtN(" + object + ")</script>")
	parsedObject := mustChallengeObject(t, page)
	legacy := &runtime{vm: goja.New()}
	want, found, evalErr := legacy.homepageChallenge(ctx, page)
	parsed, accepted := literalChallenge(parsedObject)
	require.Equal(t, fast, accepted, "AST acceptance")
	if accepted {
		require.NoError(t, evalErr)
		require.Equal(t, want, parsed, "all challenge fields")
	}
	wantErr := evalErr
	var wantID string
	if wantErr == nil {
		if !found || !validChallenge(want) {
			wantErr = &Error{Stage: ErrChallenge, Code: "invalid-challenge"}
		} else {
			wantID, wantErr = interpreterID(want)
		}
	}
	allocations := 0
	var stats metrics
	gotID, gotErr := describeInterpreter(ctx, page, func() *runtime {
		allocations++
		return &runtime{vm: goja.New(), stats: &stats}
	})
	if wantErr == nil {
		require.NoError(t, gotErr)
	} else {
		require.EqualError(t, gotErr, wantErr.Error())
		var oldFailure, newFailure *Error
		require.ErrorAs(t, wantErr, &oldFailure)
		require.ErrorAs(t, gotErr, &newFailure)
		require.Equal(t, oldFailure.Stage, newFailure.Stage)
		require.Equal(t, oldFailure.Code, newFailure.Code)
	}
	require.Equal(t, wantID, gotID)
	wantVMs := 1
	if fast {
		wantVMs = 0
	}
	require.Equal(t, wantVMs, allocations)
	require.Equal(t, int64(wantVMs), stats.challenges.Load())
	require.Zero(t, stats.initializations.Load())
	require.Zero(t, stats.executions.Load())
	return want, wantErr
}

func jsonResponseLiteral(t *testing.T, response string) string {
	t.Helper()
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	return "{R:" + string(encoded) + "}"
}

func TestLiteralChallengeDifferential(t *testing.T) {
	for _, tc := range []struct {
		name, object string
		fast         bool
	}{
		{"ordinary", `{R:{bgChallenge:` + literalFixture + `}}`, true},
		{"escapes", `{R:{bgChallenge:{'pro\u0067ram':'Привет\x20\uD83D\uDE80\u0000',globalName:'g\u0075ard',interpreterHash:'\u2028\u2029',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'a\
b'}}}}`, true},
		{"lone-surrogates", `{R:{bgChallenge:{program:'\uD800',globalName:'\uDC00',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'\uD800x\uDC00'}}}}`, true},
		{"url", `{R:{bgChallenge:{program:'p',globalName:'g',interpreterHash:'h',interpreterUrl:{privateDoNotAccessOrElseTrustedResourceUrlWrappedValue:'//www.google.com/botguard/fixture.js'}}}}`, true},
		{"invalid-url", `{R:{bgChallenge:{program:'p',globalName:'g',interpreterUrl:{privateDoNotAccessOrElseTrustedResourceUrlWrappedValue:'https://example.invalid/x'}}}}`, true},
		{"null-hash", `{R:{bgChallenge:{program:'p',globalName:'g',interpreterHash:null,interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'s'}}}}`, true},
		{"null-program", `{R:{bgChallenge:{program:null,globalName:'g'}}}`, true},
		{"null-challenge", `{R:{bgChallenge:null}}`, true},
		{"case-aliases", `{R:{bgChallenge:{Program:'first',program:'last',globalName:'g',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'s'}}}}`, true},
		{"duplicate-field", `{R:{bgChallenge:{program:'first',program:'last',globalName:'g',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'s'}}}}`, false},
		{"escaped-duplicate", `{R:{bgChallenge:{program:'first','pro\u0067ram':'last',globalName:'g',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'s'}}}}`, false},
		{"prototype", `{R:{bgChallenge:{__proto__:` + literalFixture + `}}}`, false},
		{"computed-name", `{R:{['bgChallenge']:` + literalFixture + `}}`, false},
		{"spread", `{R:{bgChallenge:{...` + literalFixture + `}}}`, false},
		{"function", `{R:{bgChallenge:(function(){return ` + literalFixture + `})()}}`, false},
		{"getter", `{R:{get bgChallenge(){return ` + literalFixture + `}}}`, false},
		{"template", "{R:{bgChallenge:{program:`p`,globalName:'g',interpreterJavascript:{privateDoNotAccessOrElseSafeScriptWrappedValue:'s'}}}}", false},
		{"array-challenge", `{R:{bgChallenge:[]}}`, false},
		{"array-field", `{R:{bgChallenge:{program:['p'],globalName:'g'}}}`, false},
		{"boolean-field", `{R:{bgChallenge:{program:true,globalName:'g'}}}`, false},
		{"undefined-field", `{R:{bgChallenge:{program:undefined,globalName:'g'}}}`, false},
		{"missing-response", `{}`, false},
		{"null-response", `{R:null}`, false},
		{"missing-challenge", `{R:{}}`, false},
		{"malformed-string", `{R:{bgChallenge:{program:'\uXYZZ'}}}`, false},
		{"malformed-number", `{R:{bgChallenge:{program:1e+}}}`, false},
		{"invalid-json-response", `{R:'{"bgChallenge":'}`, false},
		{"json-response", jsonResponseLiteral(t, `{"bgChallenge":{"program":"p","globalName":"g","interpreterHash":"h","interpreterJavascript":{"privateDoNotAccessOrElseSafeScriptWrappedValue":"s"}}}`), true},
		{"json-duplicate", jsonResponseLiteral(t, `{"bgChallenge":{"program":"first","program":"last","globalName":"g","interpreterJavascript":{"privateDoNotAccessOrElseSafeScriptWrappedValue":"s"}}}`), true},
		{"json-unicode", jsonResponseLiteral(t, `{"bgChallenge":{"pro\u0067ram":"\ud800","globalName":"\ud83d\ude80","interpreterJavascript":{"privateDoNotAccessOrElseSafeScriptWrappedValue":"\udc00"}}}`), true},
		{"json-array-field", jsonResponseLiteral(t, `{"bgChallenge":{"program":["p"]}}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkLiteralChallenge(t, tc.object, tc.fast)
		})
	}
}

var challengeNumbers = []string{
	"0", "-0", "1", "-1", "9007199254740991", "9007199254740992", "9007199254740993",
	"-9007199254740991", "-9007199254740992", "-9007199254740993",
	"9223372036854775806", "9223372036854775807", "9223372036854775808",
	"-9223372036854775807", "-9223372036854775808", "-9223372036854775809",
	"0.5", "-0.5", "1.0", "1e3", "1e20", "1e308", "1e309", "-1e309",
	"5e-324", "1e-324", "1e-400", "-1e-400", "0x20000000000001", "1n",
}

func TestLiteralChallengeNumericFieldsDifferential(t *testing.T) {
	// Every number replaces a field actually decoded into challenge. Numbers
	// must not be silently coerced to strings, even when they round in Number.
	for _, number := range challengeNumbers {
		for _, field := range []string{
			"program", "globalName", "interpreterHash", "interpreterUrl",
			"interpreterJavascript", "url-value", "script-value",
		} {
			t.Run(number+"/"+field, func(t *testing.T) {
				properties := map[string]string{
					"program": "'p'", "globalName": "'g'", "interpreterHash": "'h'",
					"interpreterUrl":        "{privateDoNotAccessOrElseTrustedResourceUrlWrappedValue:'https://www.google.com/botguard/f.js'}",
					"interpreterJavascript": "{privateDoNotAccessOrElseSafeScriptWrappedValue:'s'}",
				}
				switch field {
				case "url-value":
					properties["interpreterUrl"] = "{privateDoNotAccessOrElseTrustedResourceUrlWrappedValue:" + number + "}"
				case "script-value":
					properties["interpreterJavascript"] = "{privateDoNotAccessOrElseSafeScriptWrappedValue:" + number + "}"
				default:
					properties[field] = number
				}
				object := fmt.Sprintf("{R:{bgChallenge:{program:%s,globalName:%s,interpreterHash:%s,interpreterUrl:%s,interpreterJavascript:%s}}}",
					properties["program"], properties["globalName"], properties["interpreterHash"], properties["interpreterUrl"], properties["interpreterJavascript"])
				_, err := checkLiteralChallenge(t, object, false)
				require.Error(t, err)
			})
		}
		// JSON.parse in the original R path also uses Number semantics.
		if json.Valid([]byte(number)) {
			t.Run(number+"/json-response", func(t *testing.T) {
				object := jsonResponseLiteral(t, `{"bgChallenge":{"program":"p","globalName":"g","interpreterHash":`+number+`,"interpreterJavascript":{"privateDoNotAccessOrElseSafeScriptWrappedValue":"s"}}}`)
				_, err := checkLiteralChallenge(t, object, false)
				require.Error(t, err)
			})
		}
	}
}

func TestLiteralNumberExportDifferential(t *testing.T) {
	// Supplemental converter check: challenge currently has no numeric fields.
	// Compare JSON exports directly so int64-to-float rounding cannot be hidden
	// by converting both sides to float64 in the assertion.
	for _, number := range challengeNumbers {
		t.Run(number, func(t *testing.T) {
			p, err := parser.ParseFile(nil, "", "("+number+")", 0)
			require.NoError(t, err)
			expr := p.Body[0].(*ast.ExpressionStatement).Expression
			got, accepted := literalData(expr)
			wantAccepted := false
			if n, ok := expr.(*ast.NumberLiteral); ok {
				switch value := n.Value.(type) {
				case int64:
					wantAccepted = value >= -(1<<53-1) && value <= 1<<53-1
				case float64:
					wantAccepted = true
				}
			}
			require.Equal(t, wantAccepted, accepted, "only safe integers and binary64 literals bypass Goja")
			if !accepted {
				return // Unary expressions and BigInt retain the existing fallback.
			}
			value, err := goja.New().RunString("(" + number + ")")
			require.NoError(t, err)
			wantJSON, wantErr := json.Marshal(value.Export())
			gotJSON, gotErr := json.Marshal(got)
			if wantErr != nil {
				require.Error(t, gotErr)
			} else {
				require.NoError(t, gotErr)
				require.Equal(t, string(wantJSON), string(gotJSON))
			}
		})
	}
}
