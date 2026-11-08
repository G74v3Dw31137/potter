# Potter

[![Go Reference](https://pkg.go.dev/badge/github.com/G74v3Dw31137/potter.svg)](https://pkg.go.dev/github.com/G74v3Dw31137/potter) [![CI](https://github.com/G74v3Dw31137/potter/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/G74v3Dw31137/potter/actions/workflows/ci.yml) [![Coverage](https://img.shields.io/endpoint?url=https%3A%2F%2Fg74v3dw31137.github.io%2Fpotter%2Fcoverage.json)](https://g74v3dw31137.github.io/potter/coverage.json) [![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/G74v3Dw31137/potter/blob/main/LICENSE)

Potter is a Go library for generating Player and GVS Proof of Origin (PO) tokens
for anonymous YouTube MWEB sessions. It handles token generation and session
management; your application makes the Player API and media requests.

JavaScript assets are embedded and run inside Go. Node.js and Python are not
runtime dependencies. Token generation requires network access to YouTube and
Google.

## Installation

Requires **Go 1.27+**.

```sh
go get github.com/G74v3Dw31137/potter
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/G74v3Dw31137/potter"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	client, err := potter.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	videoID := "dQw4w9WgXcQ"
	proofs, err := client.Tokens(ctx, videoID)
	if err != nil {
		return err
	}

	// Print sizes and the client version, never tokens or visitor identity.
	fmt.Printf("Player: %d bytes; GVS: %d bytes; visitor data: %d bytes; MWEB: %s\n",
		len(proofs.Player), len(proofs.GVS), len(proofs.VisitorData), proofs.ClientVersion)
	return nil
}
```

## Tokens and session identity

`Tokens` returns both tokens and their MWEB session information:

- Put `Player` in `serviceIntegrityDimensions.poToken` in the Player request.
  Player tokens are bound to the requested video ID.
- Put `GVS` in the `pot` query parameter of a conventional media URL. Depending
  on the session configuration, it is bound to either the video ID or visitor
  data. `GVSBinding` contains the video ID or visitor data used to bind the GVS token.
- Use `ClientVersion` and `VisitorData` for the MWEB client version and visitor
  identity, together with `potter.UserAgent` as the user agent.

Keep this identity and the network identity used during generation consistent
when making Player and media requests. If you use a proxy, use the same network
route for token generation and those requests. Use the session values returned
with each token pair, since the client may refresh its session.

Do not log tokens, visitor data, cookies, or signed media URLs.

## Client lifecycle

Reuse a client across calls. `NewClient` performs no network I/O; the first
`Tokens` call initializes the session. Call `Warmup(ctx)` if you want to initialize
it ahead of time. The client manages session refresh and token caching.

Public operations are safe to call concurrently and are serialized internally.
Pass a non-nil context to `Warmup` and `Tokens`. Canceling a call can stop waiting
while shared initialization continues. `Close` cancels owned work and waits for
it to finish; cancellation and shutdown are cooperative.

For custom HTTP configuration, pass
`potter.WithHTTPClient(httpClient)` to `NewClient`. Potter copies the HTTP client's
policy and shares its transport. Use an anonymous client without credentials or
cookies, with a transport that honors request contexts. `Close` does not close
the caller's transport.

## Error handling

Use `errors.Is` to check sentinel errors such as `potter.ErrClosed` or
`potter.ErrBinding`, and context cancellation or deadline errors. Use `errors.As`
with a `*potter.Error` to inspect its `Stage`, `Code`, and optional `StatusCode`.
Failed calls return no partial tokens.

## Limitations

Potter supports anonymous MWEB sessions only. YouTube changes can break token
generation, and generated tokens have no guaranteed acceptance or lifetime.

Potter is not a media extractor. It does not make Player API or media requests,
decipher signatures, transform the `n` parameter, or implement SABR.

## Development

Run the ordinary tests and checks offline, with Go dependencies already cached:

```sh
GOWORK=off GOPROXY=off GOSUMDB=off go test ./...
GOWORK=off GOPROXY=off GOSUMDB=off go test -race ./...
GOWORK=off GOPROXY=off GOSUMDB=off go vet ./...
```

The race detector requires a supported C toolchain. Maintainers can verify the
embedded JavaScript bundles without rewriting them by running
`GOWORK=off GOPROXY=off GOSUMDB=off go run . -check` from `tools/botguard-bundle`.

## Documentation

API reference: [pkg.go.dev](https://pkg.go.dev/github.com/G74v3Dw31137/potter).

## License

Potter's original code is licensed under the [MIT License](LICENSE). Third-party
components retain their respective licenses; see [THIRD_PARTY.md](THIRD_PARTY.md)
for notices and redistribution requirements, including those for binaries.
