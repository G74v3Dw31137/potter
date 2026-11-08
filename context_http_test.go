package potter

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type homepageTransport func(*http.Request) (*http.Response, error)

func (f homepageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHomepageHTTPBoundary(t *testing.T) {
	for _, target := range []string{"https://www.youtube.com/", "https://unapproved.invalid/?PRIVATE", "http://m.youtube.com/", "https://m.youtube.com:8443/", "https://PRIVATE@m.youtube.com/", "https://evilyoutube.com/"} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }, Transport: homepageTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				status := 200
				h := make(http.Header)
				if calls == 1 {
					require.Equal(t, homepage, r.URL.String())
					require.Equal(t, "PRIVATE-visitor", r.Header.Get("X-Goog-Visitor-Id"))
					h.Set("Location", target)
					status = 302
				} else {
					require.Empty(t, r.Header.Get("X-Goog-Visitor-Id"))
				}
				page := `<script>ytcfg.set({"INNERTUBE_CONTEXT":{"client":{"clientName":"MWEB","clientVersion":"fixture-version","visitorData":"PRIVATE-visitor"}},"WEB_PLAYER_CONTEXT_CONFIGS":{"WEB_PLAYER_CONTEXT_CONFIG_ID_MWEB_WATCH":{"serializedExperimentFlags":""}}});window.ytAtN({R:{bgChallenge:{program:'fixture-program',globalName:'testGuard',interpreterUrl:{privateDoNotAccessOrElseTrustedResourceUrlWrappedValue:'https://www.google.com/botguard/fixture.js'}}}});</script>`
				return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(page)), Request: r}, nil
			})}
			c, err := NewClient(WithHTTPClient(hc))
			require.NoError(t, err)
			defer c.Close()
			page, err := c.fetchContext(context.Background(), "PRIVATE-visitor")
			if target == "https://www.youtube.com/" {
				require.NoError(t, err, "approved response should reach homepage parser")
				require.Equal(t, "PRIVATE-visitor", page.guard.Visitor)
				require.Equal(t, 2, calls)
			} else {
				require.ErrorIs(t, err, ErrChallenge)
				require.Equal(t, 1, calls, "forbidden homepage redirect reached transport")
				require.NotContains(t, err.Error(), "PRIVATE")
			}
		})
	}
}
