package potter_test

import (
	"context"
	"time"

	"github.com/G74v3Dw31137/potter"
)

// This example is compiled, but is not executed by go test (it has no Output).
func ExampleClient_Tokens() {
	client, err := potter.NewClient()
	if err != nil {
		return
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	proofs, err := client.Tokens(ctx, "jNQXAC9IVRw")
	if err != nil {
		return
	}
	// Use proofs.Player in serviceIntegrityDimensions.poToken and proofs.GVS
	// in the media URL's pot parameter, with the matching MWEB identity.
	// Do not log proofs or visitor data.
	_ = proofs
}
