package potter_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter"
	"github.com/stretchr/testify/require"
)

type errorTransport func(*http.Request) (*http.Response, error)

func (f errorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedReader struct{ err error }

func (r failedReader) Read([]byte) (int, error) { return 0, r.err }

type transportTimeout struct{}

func (transportTimeout) Error() string   { return "PRIVATE timeout" }
func (transportTimeout) Timeout() bool   { return true }
func (transportTimeout) Temporary() bool { return true }

func requestStage(r *http.Request) error {
	if r.Method == http.MethodPost {
		return potter.ErrGenerateIT
	}
	if r.URL.Path == "/" {
		return potter.ErrChallenge
	}
	return potter.ErrInterpreter
}

func TestPublicErrorPropagation(t *testing.T) {
	for _, stage := range []error{potter.ErrChallenge, potter.ErrInterpreter, potter.ErrGenerateIT} {
		for _, tc := range []struct {
			name, code string
			cause      error
			read       bool
		}{
			{"canceled", "canceled", context.Canceled, false},
			{"deadline", "deadline", context.DeadlineExceeded, false},
			{"read-canceled", "canceled", context.Canceled, true},
			{"read-deadline", "deadline", context.DeadlineExceeded, true},
			{"dns", "dns", &net.DNSError{Name: "PRIVATE.invalid", Err: "PRIVATE"}, false},
			{"tls", "tls", tls.RecordHeaderError{Msg: "PRIVATE TLS"}, false},
			{"timeout-without-deadline", "transport-timeout", transportTimeout{}, false},
			{"unrelated-context-text", "transport", errors.New("PRIVATE context canceled deadline exceeded"), false},
			{"read", "body-read", io.ErrUnexpectedEOF, true},
		} {
			for _, warm := range []bool{false, true} {
				t.Run(fmt.Sprint(stage, "/", tc.name, "/warm=", warm), func(t *testing.T) {
					_, fixture := newFixture(t, "html5_generate_content_po_token=true")
					closed := false
					client, err := potter.NewClient(potter.WithHTTPClient(&http.Client{Transport: errorTransport(func(r *http.Request) (*http.Response, error) {
						if requestStage(r) != stage {
							return fixture.RoundTrip(r)
						}
						cause := fmt.Errorf("PRIVATE https://secret.invalid: %w", tc.cause)
						if !tc.read {
							return nil, cause
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &trackedBody{Reader: failedReader{cause}, close: func() { closed = true }}, Request: r}, nil
					})}))
					require.NoError(t, err)
					defer client.Close()
					if warm {
						err = client.Warmup(context.Background())
					} else {
						var tokens potter.Tokens
						tokens, err = client.Tokens(context.Background(), "video")
						require.Equal(t, potter.Tokens{}, tokens)
					}
					require.ErrorIs(t, err, stage)
					var classified *potter.Error
					require.ErrorAs(t, err, &classified)
					require.Equal(t, tc.code, classified.Code)
					for _, identity := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF} {
						require.Equal(t, errors.Is(tc.cause, identity), errors.Is(err, identity))
					}
					require.NotContains(t, err.Error(), "PRIVATE")
					require.NotContains(t, err.Error(), "https://")
					var rawURL *net.DNSError
					require.False(t, errors.As(err, &rawURL), "raw DNS identity leaks private names")
					require.Equal(t, tc.read, closed)
				})
			}
		}
	}
}

func TestPublicHTTPClientDeadline(t *testing.T) {
	for _, stage := range []error{potter.ErrChallenge, potter.ErrInterpreter, potter.ErrGenerateIT} {
		for _, read := range []bool{false, true} {
			t.Run(fmt.Sprint(stage, "/read=", read), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					_, fixture := newFixture(t, "html5_generate_content_po_token=true")
					client, err := potter.NewClient(potter.WithHTTPClient(&http.Client{Timeout: time.Second, Transport: errorTransport(func(r *http.Request) (*http.Response, error) {
						if requestStage(r) != stage {
							return fixture.RoundTrip(r)
						}
						if read {
							return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(waitForCancellation{r.Context()}), Request: r}, nil
						}
						<-r.Context().Done()
						return nil, r.Context().Err()
					})}))
					require.NoError(t, err)
					defer client.Close()
					ctx := context.Background()
					err = client.Warmup(ctx)
					require.NoError(t, ctx.Err(), "caller context must remain active")
					require.ErrorIs(t, err, context.DeadlineExceeded)
					require.ErrorIs(t, err, stage)
				})
			})
		}
	}
}

type waitForCancellation struct{ ctx context.Context }

func (r waitForCancellation) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestPublicHomepageResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"oversize", strings.Repeat("x", (8<<20)+1), "response-too-large", 200},
		{"status", "PRIVATE body", "http-status", 503},
		{"malformed", "<script>ytcfg.set({PRIVATE invalid JSON});</script>", "invalid-homepage-config", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed := false
			client, err := potter.NewClient(potter.WithHTTPClient(&http.Client{Transport: errorTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: &trackedBody{Reader: strings.NewReader(tc.body), close: func() { closed = true }}, Request: r}, nil
			})}))
			require.NoError(t, err)
			defer client.Close()
			err = client.Warmup(context.Background())
			require.ErrorIs(t, err, potter.ErrChallenge)
			var classified *potter.Error
			require.ErrorAs(t, err, &classified)
			require.Equal(t, tc.code, classified.Code)
			require.True(t, closed)
			require.NotContains(t, err.Error(), "PRIVATE")
		})
	}
}
