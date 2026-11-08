package botguard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type securityTransport func(*http.Request) (*http.Response, error)

func (f securityTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func securityResponse(r *http.Request, status int, location string) *http.Response {
	h := make(http.Header)
	if location != "" {
		h.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader("ok")), Request: r}
}

func TestHTTPDestinations(t *testing.T) {
	for _, category := range []struct {
		name, method, valid string
		stage               error
	}{
		{"homepage", "GET", "https://m.youtube.com/", ErrChallenge},
		{"interpreter", "GET", "https://www.google.com/botguard/fixture.js", ErrInterpreter},
		{"integrity", "POST", rpcBase + "GenerateIT", ErrGenerateIT},
	} {
		t.Run(category.name, func(t *testing.T) {
			u, err := url.Parse(category.valid)
			require.NoError(t, err)
			for _, tc := range []struct {
				name, target string
				allowed      bool
			}{
				{"valid", category.valid, true},
				{"explicit443", strings.Replace(category.valid, u.Host, u.Host+":443", 1), true},
				{"uppercase", strings.Replace(category.valid, u.Host, strings.ToUpper(u.Host), 1), true},
				{"unapproved", "https://unapproved.invalid" + u.Path + "?secret=PRIVATE", false},
				{"downgrade", strings.Replace(category.valid, "https:", "http:", 1), false},
				{"suffix", strings.Replace(category.valid, u.Host, "evilyoutube.com", 1), false},
				{"subdomain", strings.Replace(category.valid, u.Host, u.Host+".evil.invalid", 1), false},
				{"port", strings.Replace(category.valid, u.Host, u.Host+":8443", 1), false},
				{"noncanonicalPort", strings.Replace(category.valid, u.Host, u.Host+":0443", 1), false},
				{"emptyPort", strings.Replace(category.valid, u.Host, u.Host+":", 1), false},
				{"userinfo", strings.Replace(category.valid, u.Host, "PRIVATE:SECRET@"+u.Host, 1), false},
				{"trailingDot", strings.Replace(category.valid, u.Host, u.Host+".", 1), false},
				{"malformed", "https://[invalid", false},
				{"relative", "/botguard/fixture.js", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					calls := 0
					hc := &http.Client{Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						require.Equal(t, "PRIVATE-visitor", r.Header.Get("X-Goog-Visitor-Id"))
						return securityResponse(r, 200, ""), nil
					})}
					m := &Engine{http: hc}
					var payload any
					if category.method == "POST" {
						payload = []string{requestKey, "PRIVATE-snapshot"}
					}
					_, err := m.request(context.Background(), Context{Visitor: "PRIVATE-visitor"}, category.method, tc.target, payload, category.stage)
					if tc.allowed {
						require.NoError(t, err)
						require.Equal(t, 1, calls)
					} else {
						require.ErrorIs(t, err, category.stage)
						require.NotContains(t, err.Error(), "PRIVATE")
						require.NotContains(t, err.Error(), tc.target)
						require.Zero(t, calls, "forbidden destination reached transport")
					}
				})
			}
		})
	}
}

func TestHTTPRequestCategoryConstraints(t *testing.T) {
	for _, tc := range []struct {
		method, target string
		stage          error
	}{
		{"GET", "https://www.google.com/", ErrChallenge},
		{"GET", "https://m.youtube.com/youtubei/v1/player", ErrChallenge},
		{"POST", "https://m.youtube.com/", ErrChallenge},
		{"GET", "https://www.google.com/", ErrInterpreter},
		{"POST", "https://www.google.com/botguard/a.js", ErrInterpreter},
		{"GET", rpcBase + "GenerateIT", ErrInterpreter},
		{"GET", rpcBase + "GenerateIT", ErrGenerateIT},
		{"POST", rpcBase + "Create", ErrGenerateIT},
		{"POST", rpcBase + "GenerateIT?PRIVATE", ErrGenerateIT},
		{"POST", rpcBase + "GenerateIT#PRIVATE", ErrGenerateIT},
	} {
		t.Run(tc.method+"/"+tc.target+"/"+tc.stage.Error(), func(t *testing.T) {
			calls := 0
			m := &Engine{http: &http.Client{Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return securityResponse(r, 200, ""), nil
			})}}
			_, err := m.request(context.Background(), Context{Visitor: "PRIVATE"}, tc.method, tc.target, nil, tc.stage)
			require.ErrorIs(t, err, tc.stage)
			require.Zero(t, calls, "request escaped its category")
			require.NotContains(t, err.Error(), "PRIVATE")
		})
	}
}

// Route approved logical HTTPS URLs to a local TLS server. The client itself
// still processes Location, method conversion, GetBody and header forwarding.
func TestHTTPRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, category := range []struct {
			name, method, initial, same, cross string
			stage                              error
		}{
			{"homepage", "GET", "https://m.youtube.com/", "https://m.youtube.com/?next=1", "https://www.youtube.com/", ErrChallenge},
			{"interpreter", "GET", "https://www.google.com/botguard/a.js", "https://www.google.com/botguard/b.js", "https://www.youtube.com/s/b.js", ErrInterpreter},
			{"integrity", "POST", rpcBase + "GenerateIT", rpcBase + "GenerateIT", "https://www.youtube.com/s/b.js", ErrGenerateIT},
		} {
			for _, destination := range []struct {
				name, target string
				approved     bool
			}{
				{"same", category.same, true},
				{"crossApproved", category.cross, true},
				{"crossForbidden", "https://unapproved.invalid/leak?PRIVATE", false},
				{"downgrade", "http://www.google.com/botguard/b.js", false},
				{"port", "https://www.google.com:8443/botguard/b.js", false},
				{"userinfo", "https://PRIVATE@www.google.com/botguard/b.js", false},
				{"suffix", "https://evilyoutube.com/botguard/b.js", false},
			} {
				t.Run(category.name+"/"+strconv.Itoa(status)+"/"+destination.name, func(t *testing.T) {
					var serverCalls atomic.Int64
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if serverCalls.Add(1) == 1 {
							w.Header().Set("Location", destination.target)
							w.WriteHeader(status)
							return
						}
						_, _ = io.WriteString(w, "ok")
					}))
					defer server.Close()
					wireURL, err := url.Parse(server.URL)
					require.NoError(t, err)
					calls := 0
					redirectMethod, redirectBody := "", ""
					transport := securityTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						if calls == 1 {
							require.Equal(t, "PRIVATE-visitor", r.Header.Get("X-Goog-Visitor-Id"))
							if category.method == "POST" {
								body, err := io.ReadAll(r.Body)
								require.NoError(t, err)
								require.Contains(t, string(body), "PRIVATE-snapshot")
								r.Body = io.NopCloser(strings.NewReader(string(body)))
							}
						} else {
							redirectMethod = r.Method
							if r.Body != nil {
								body, e := io.ReadAll(r.Body)
								require.NoError(t, e)
								redirectBody = string(body)
								r.Body = io.NopCloser(strings.NewReader(redirectBody))
							}
							if destination.approved && category.method == "GET" {
								require.Empty(t, r.Header.Get("X-Goog-Visitor-Id"), "redirect leaked custom identity header")
								require.Empty(t, r.Header.Get("X-Goog-Api-Key"))
								require.Empty(t, r.Header.Get("X-User-Agent"))
								require.Empty(t, r.Header.Get("Referer"))
								require.Equal(t, "GET", r.Method)
								require.Empty(t, redirectBody)
							}
						}
						forward := r.Clone(r.Context())
						forward.URL.Scheme, forward.URL.Host = wireURL.Scheme, wireURL.Host
						forward.Host = r.URL.Host
						return server.Client().Transport.RoundTrip(forward)
					})
					m := &Engine{http: &http.Client{Transport: transport}}
					var payload any
					if category.method == "POST" {
						payload = []string{requestKey, "PRIVATE-snapshot"}
					}
					_, err = m.request(context.Background(), Context{Visitor: "PRIVATE-visitor"}, category.method, category.initial, payload, category.stage)
					if destination.approved && category.method == "GET" {
						require.NoError(t, err)
						require.Equal(t, 2, calls)
					} else {
						require.Equal(t, 1, calls, "forbidden redirect reached transport: method=%s snapshotReplay=%v", redirectMethod, strings.Contains(redirectBody, "PRIVATE-snapshot"))
						require.EqualValues(t, 1, serverCalls.Load(), "forbidden destination received a request")
						require.ErrorIs(t, err, category.stage)
						require.NotContains(t, err.Error(), "PRIVATE")
					}
				})
			}
		}
	}
}

func TestHTTPRedirectChainLimit(t *testing.T) {
	for _, loop := range []bool{false, true} {
		t.Run(strconv.FormatBool(loop), func(t *testing.T) {
			calls := 0
			m := &Engine{http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }, Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.LessOrEqual(t, calls, 6, "Potter redirect limit bypassed")
				target := "/botguard/loop.js"
				if !loop {
					target += "?hop=" + strconv.Itoa(calls)
				}
				return securityResponse(r, 302, target), nil
			})}}
			_, err := m.request(context.Background(), Context{}, "GET", "https://www.google.com/botguard/loop.js", nil, ErrInterpreter)
			require.ErrorIs(t, err, ErrInterpreter)
			require.Equal(t, 6, calls, "allow at most five redirects")
		})
	}
}

func TestHTTPCallerRedirectPolicy(t *testing.T) {
	for _, action := range []string{"allow", "deny", "lastResponse", "mutateURL", "mutateHost", "mutateMethod", "mutateBody", "mutateHeader", "cancel", "forbidden"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, callbacks := 0, 0
			jar, err := cookiejar.New(nil)
			require.NoError(t, err)
			u, _ := url.Parse("https://www.google.com/")
			jar.SetCookies(u, []*http.Cookie{{Name: "caller", Value: "cookie", Secure: true}})
			callback := func(r *http.Request, via []*http.Request) error {
				callbacks++
				require.Len(t, via, 1)
				switch action {
				case "deny":
					return errors.New("PRIVATE callback URL and visitor")
				case "lastResponse":
					return http.ErrUseLastResponse
				case "mutateURL":
					r.URL, _ = url.Parse("https://forbidden.invalid/?PRIVATE")
				case "mutateHost":
					r.Host = "forbidden.invalid"
				case "mutateMethod":
					r.Method = "POST"
				case "mutateBody":
					r.Body = io.NopCloser(strings.NewReader("PRIVATE-snapshot"))
				case "mutateHeader":
					r.Header["x-goog-visitor-id"] = []string{"PRIVATE-injected"}
				case "cancel":
					cancel()
				}
				return nil
			}
			transport := securityTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "caller=cookie", r.Header.Get("Cookie"))
				if calls == 1 {
					target := "/botguard/next.js"
					if action == "forbidden" {
						target = "https://forbidden.invalid/"
					}
					return securityResponse(r, 302, target), nil
				}
				for key := range r.Header {
					require.NotEqual(t, "x-goog-visitor-id", strings.ToLower(key))
				}
				return securityResponse(r, 200, ""), nil
			})
			hc := &http.Client{Transport: transport, CheckRedirect: callback, Jar: jar}
			m := &Engine{http: hc}
			_, err = m.request(ctx, Context{Visitor: "PRIVATE-visitor"}, "GET", "https://www.google.com/botguard/start.js", nil, ErrInterpreter)
			if action == "allow" || action == "mutateHeader" {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
			} else {
				if action == "cancel" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.ErrorIs(t, err, ErrInterpreter)
				}
				require.Equal(t, 1, calls)
				require.NotContains(t, err.Error(), "PRIVATE")
			}
			if action == "forbidden" {
				require.Zero(t, callbacks)
			} else {
				require.Equal(t, 1, callbacks)
			}
			require.Same(t, jar, hc.Jar)
			require.Equal(t, reflect.ValueOf(callback).Pointer(), reflect.ValueOf(hc.CheckRedirect).Pointer())
			require.Equal(t, reflect.ValueOf(transport).Pointer(), reflect.ValueOf(hc.Transport).Pointer())
			require.Len(t, jar.Cookies(u), 1)
		})
	}
}

func TestHTTPIntegrityRedirectCannotBeApproved(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls, callbacks := 0, 0
			hc := &http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error { callbacks++; return nil },
				Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					require.Equal(t, 1, calls, "caller approved an integrity replay")
					return securityResponse(r, status, rpcBase+"GenerateIT"), nil
				}),
			}
			m := &Engine{http: hc}
			_, err := m.request(context.Background(), Context{Visitor: "PRIVATE"}, "POST", rpcBase+"GenerateIT", []string{requestKey, "PRIVATE-snapshot"}, ErrGenerateIT)
			require.ErrorIs(t, err, ErrGenerateIT)
			require.Equal(t, 1, calls)
			require.Zero(t, callbacks)
		})
	}
}

type redirectMutatingJar struct{ reads int }

func (j *redirectMutatingJar) Cookies(u *url.URL) []*http.Cookie {
	j.reads++
	if j.reads == 2 {
		u.Host = "forbidden.invalid"
	}
	return nil
}
func (*redirectMutatingJar) SetCookies(*url.URL, []*http.Cookie) {}

func TestHTTPTransportValidatesAfterCookieProcessing(t *testing.T) {
	// A URL mutation after CheckRedirect must still be rejected before the
	// supplied transport runs. Ordinary jars do not mutate their input URL.
	jar := &redirectMutatingJar{}
	calls := 0
	hc := &http.Client{Jar: jar, Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, 1, calls, "post-callback destination reached transport")
		return securityResponse(r, 302, "/botguard/next.js"), nil
	})}
	m := &Engine{http: hc}
	_, err := m.request(context.Background(), Context{}, "GET", "https://www.google.com/botguard/start.js", nil, ErrInterpreter)
	require.ErrorIs(t, err, ErrInterpreter)
	require.Equal(t, 1, calls)
	require.Equal(t, 2, jar.reads)
	require.Same(t, jar, hc.Jar)
}

type securityBody struct {
	io.Reader
	closed bool
}

func (b *securityBody) Close() error { b.closed = true; return nil }

func TestHTTPResponseClosureAndRedaction(t *testing.T) {
	for _, action := range []string{"redirect", "lastResponse", "malformedRedirect", "transportError", "oversize"} {
		t.Run(action, func(t *testing.T) {
			body := &securityBody{Reader: strings.NewReader("PRIVATE")}
			if action == "oversize" {
				body.Reader = io.LimitReader(repeatedHTTPByte{}, (8<<20)+1)
			}
			hc := &http.Client{Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
				if action == "transportError" {
					return nil, errors.New("PRIVATE https://secret.invalid/?visitor=PRIVATE")
				}
				resp := securityResponse(r, 302, "https://forbidden.invalid/?PRIVATE")
				if action == "malformedRedirect" {
					resp.Header.Set("Location", "https://[PRIVATE")
				}
				if action == "oversize" {
					resp.StatusCode = 200
				}
				if action == "lastResponse" {
					resp.Header.Set("Location", "/botguard/next.js")
				}
				resp.Body = body
				return resp, nil
			})}
			if action == "lastResponse" {
				hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			}
			m := &Engine{http: hc}
			_, err := m.request(context.Background(), Context{}, "GET", "https://www.google.com/botguard/start.js", nil, ErrInterpreter)
			require.ErrorIs(t, err, ErrInterpreter)
			require.NotContains(t, err.Error(), "PRIVATE")
			require.NotContains(t, err.Error(), "https://")
			if action != "transportError" {
				require.True(t, body.closed, "response body left open")
			}
		})
	}
}

type repeatedHTTPByte struct{}

func (repeatedHTTPByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestHTTPRedirectHeadersStayRemoved(t *testing.T) {
	targets := []string{"https://www.google.com/botguard/start.js", "https://www.youtube.com/s/next.js", "https://www.google.com/botguard/end.js"}
	calls := 0
	m := &Engine{http: &http.Client{Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, targets[calls], r.URL.String())
		if calls > 0 {
			require.Empty(t, r.Header.Get("X-Goog-Visitor-Id"), "initial headers restored on a later hop")
			require.Empty(t, r.Header.Get("Referer"))
		}
		calls++
		if calls < len(targets) {
			return securityResponse(r, 307, targets[calls]), nil
		}
		return securityResponse(r, 200, ""), nil
	})}}
	_, err := m.request(context.Background(), Context{Visitor: "PRIVATE"}, "GET", targets[0], nil, ErrInterpreter)
	require.NoError(t, err)
	require.Equal(t, 3, calls)
}

func TestHTTPRedirectCancellationAndTimeout(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(strconv.FormatBool(timeout), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int64
			entered := make(chan struct{})
			hc := &http.Client{Transport: securityTransport(func(r *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					return securityResponse(r, 302, "/botguard/next.js"), nil
				}
				close(entered)
				<-r.Context().Done()
				return nil, errors.New("PRIVATE transport error")
			})}
			if timeout {
				hc.Timeout = 100 * time.Millisecond
			}
			m := &Engine{http: hc}
			done := make(chan error, 1)
			go func() {
				_, err := m.request(ctx, Context{}, "GET", "https://www.google.com/botguard/start.js", nil, ErrInterpreter)
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("redirect not reached")
			}
			if !timeout {
				cancel()
			}
			select {
			case err := <-done:
				if timeout {
					require.ErrorIs(t, err, ErrInterpreter)
				} else {
					require.ErrorIs(t, err, context.Canceled)
				}
				require.NotContains(t, err.Error(), "PRIVATE")
			case <-time.After(5 * time.Second):
				t.Fatal("redirect ignored cancellation or timeout")
			}
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestInterpreterDestinationDescription(t *testing.T) {
	for _, target := range []string{"https://unapproved.invalid/private", "https://evilyoutube.com/private", "https://www.google.com:8443/private"} {
		ch := challenge{}
		ch.InterpreterUrl.Value = target
		_, err := interpreterID(ch)
		require.ErrorIs(t, err, ErrInterpreter)
		require.NotContains(t, err.Error(), target)
	}
	for _, target := range []string{"//www.google.com/botguard/a.js", "https://m.youtube.com/s/a.js", "https://www.youtube.com/s/a.js"} {
		ch := challenge{}
		ch.InterpreterUrl.Value = target
		_, err := interpreterID(ch)
		require.NoError(t, err)
	}
}
