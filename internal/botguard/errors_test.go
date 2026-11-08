package botguard

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/require"
)

func TestErrorFormatting(t *testing.T) {
	for _, e := range []*Error{nil, {}, {Code: "reason"}, {StatusCode: 503}, {Stage: ErrMint, Code: "empty-token"}} {
		require.NotPanics(t, func() { require.NotEmpty(t, e.Error()) })
		require.NotPanics(t, func() { _ = errors.Unwrap(e) })
	}
	e := &Error{Stage: ErrGenerateIT, Code: "http-status", StatusCode: 503}
	require.Equal(t, "BotGuard GenerateIT failed: HTTP 503 (http-status)", e.Error())
	require.ErrorIs(t, e, ErrGenerateIT)
}

func TestRuntimeErrorPropagation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			err := rt.run(context.Background(), ErrSnapshot, func(context.Context) error {
				return fmt.Errorf("PRIVATE detail: %w", cause)
			})
			require.ErrorIs(t, err, cause)
			require.ErrorIs(t, err, ErrSnapshot)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.True(t, rt.broken)
		})
	}
}

func TestRuntimeWrappedClassificationRedaction(t *testing.T) {
	rt := newRuntime(fixtureContext())
	err := rt.run(context.Background(), ErrMint, func(context.Context) error {
		return fmt.Errorf("PRIVATE wrapper: %w", Failure(ErrMint, "empty-token", context.Canceled))
	})
	require.ErrorIs(t, err, ErrMint)
	require.ErrorIs(t, err, context.Canceled)
	var classified *Error
	require.ErrorAs(t, err, &classified)
	require.Equal(t, "empty-token", classified.Code)
	require.NotContains(t, err.Error(), "PRIVATE")
}

func TestMintInvalidOutput(t *testing.T) {
	for _, tc := range []struct{ value, code string }{
		{`''`, "empty-token"},
		{`null`, "non-string-token"},
		{`0`, "non-string-token"},
		{`{}`, "non-string-token"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			rt := newRuntime(fixtureContext())
			require.NoError(t, rt.eval(`var minter={mintAsWebsafeString(){return Promise.resolve(`+tc.value+`)}}`))
			token, err := rt.mint(context.Background(), "binding")
			require.Empty(t, token)
			require.ErrorIs(t, err, ErrMint)
			var classified *Error
			require.ErrorAs(t, err, &classified)
			require.Equal(t, tc.code, classified.Code)
			var js *goja.Exception
			require.False(t, errors.As(err, &js))
		})
	}
}

func TestMalformedChallengeErrors(t *testing.T) {
	for _, page := range []string{
		`window.ytAtN({R:{bgChallenge:{}}})`,
		`window.ytAtN({R:'PRIVATE invalid JSON'})`,
		`window.ytAtN({R:{bgChallenge:{program:123}}})`,
		`window.ytAtN({R:{bgChallenge:`,
	} {
		_, err := DescribeInterpreter(context.Background(), []byte("<script>"+page+"</script>"))
		require.ErrorIs(t, err, ErrChallenge)
		var classified *Error
		require.ErrorAs(t, err, &classified)
		require.NotEmpty(t, classified.Code)
		require.NotContains(t, err.Error(), "PRIVATE")
		require.False(t, errors.Is(err, context.Canceled))
		require.False(t, errors.Is(err, context.DeadlineExceeded))
	}
}

func TestInterpreterCompileError(t *testing.T) {
	engine, _ := newFake(t)
	ch := challenge{Program: "program", GlobalName: "guard"}
	ch.InterpreterJavascript.Value = `function PRIVATE invalid syntax`
	_, err := engine.interpreter(context.Background(), fixtureContext(), ch)
	require.ErrorIs(t, err, ErrInterpreter)
	var classified *Error
	require.ErrorAs(t, err, &classified)
	require.Equal(t, "javascript-compile", classified.Code)
	require.NotContains(t, err.Error(), "PRIVATE")
	var js *goja.Exception
	require.False(t, errors.As(err, &js))
}
