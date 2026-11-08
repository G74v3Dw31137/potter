package potter_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/G74v3Dw31137/potter"
	"github.com/stretchr/testify/require"
)

func TestPublicUndefinedRejection(t *testing.T) {
	for _, role := range []string{"player", "gvs"} {
		t.Run(role, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c, f := newFixture(t, "html5_generate_content_po_token=true")
				f.script = []byte(strings.Replace(string(f.script), "if(text==='reject')", "if(text==='undefined-reject')return Promise.reject(undefined);if(text==='reject')", 1))
				video := "undefined-reject"
				if role == "gvs" {
					video = "video"
					f.visitor = "undefined-reject"
					f.page = fixturePage("html5_generate_content_po_token=false", f.visitor)
				}
				start := time.Now()
				got, err := c.Tokens(context.Background(), video)
				require.Equal(t, potter.Tokens{}, got)
				require.ErrorIs(t, err, potter.ErrMint)
				require.NotErrorIs(t, err, context.DeadlineExceeded)
				require.NotErrorIs(t, err, context.Canceled)
				var classified *potter.Error
				require.ErrorAs(t, err, &classified)
				require.Equal(t, "promise-rejected", classified.Code)
				require.NotContains(t, err.Error(), "PRIVATE")
				require.Equal(t, start, time.Now(), "settled rejection must not consume the execution deadline")
				calls(t, f, "integrity", 1)
				got, err = c.Tokens(context.Background(), "healthy")
				require.ErrorIs(t, err, potter.ErrMint)
				require.Equal(t, potter.Tokens{}, got)
				calls(t, f, "integrity", 1) // failed generation is unusable during backoff
				f.mu.Lock()
				f.page = fixturePage("html5_generate_content_po_token=true", "healthy-identity")
				f.visitor = "healthy-identity"
				f.mu.Unlock()
				time.Sleep(5 * time.Second) // virtual policy backoff, never used to establish settlement
				got, err = c.Tokens(context.Background(), "healthy")
				require.NoError(t, err)
				require.Equal(t, "healthy-identity", got.VisitorData)
				require.Equal(t, "fixture:1:healthy", decode(t, got.Player))
				calls(t, f, "integrity", 2) // new runtime starts with a fresh mint counter/cache
			})
		})
	}
}
