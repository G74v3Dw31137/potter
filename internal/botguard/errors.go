package botguard

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
)

var (
	ErrChallenge   = errors.New("BotGuard challenge failed")
	ErrInterpreter = errors.New("BotGuard interpreter failed")
	ErrRuntime     = errors.New("BotGuard runtime initialization failed")
	ErrSnapshot    = errors.New("BotGuard snapshot failed")
	ErrGenerateIT  = errors.New("BotGuard GenerateIT failed")
	ErrIntegrity   = errors.New("BotGuard integrity token missing or invalid")
	ErrMint        = errors.New("BotGuard mint failed")
	ErrExpired     = errors.New("BotGuard session expired")
	ErrIdentity    = errors.New("BotGuard client identity superseded")
	ErrBinding     = errors.New("BotGuard GVS binding configuration unavailable")
	ErrClosed      = errors.New("client closed")
)

// Error contains a stage and optional HTTP status, never opaque server/JS data.
type Error struct {
	Code       string
	Stage      error
	StatusCode int
}

func (e *Error) Error() string {
	message := "BotGuard operation failed"
	if e == nil {
		return message
	}
	if e.Stage != nil {
		message = e.Stage.Error()
	}
	if e.StatusCode != 0 {
		message += fmt.Sprintf(": HTTP %d", e.StatusCode)
	}
	if e.Code != "" {
		message += " (" + e.Code + ")"
	}
	return message
}
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Stage
}

// Failure retains only reviewed, data-free causes. In particular, transport
// errors can contain URLs/headers and Goja errors can contain private JS values;
// neither may be reachable through the public chain. Codes are fixed at callers.
func Failure(stage error, code string, cause error) error {
	return withSafeCause(&Error{Stage: stage, Code: code}, cause)
}

func withSafeCause(safe *Error, cause error) error {
	for _, identity := range []error{context.Canceled, context.DeadlineExceeded, io.ErrUnexpectedEOF, io.EOF} {
		if errors.Is(cause, identity) {
			return fmt.Errorf("%w: %w", safe, identity)
		}
	}
	return safe
}

// HTTPFailure classifies network failures without retaining private error data.
// A net.Error timeout alone does not establish context.DeadlineExceeded.
func HTTPFailure(ctx context.Context, stage error, reading bool, cause error) error {
	code := "transport"
	if reading {
		code = "body-read"
	} else {
		// url.Error.Timeout only inspects its immediate cause; a %w wrapper
		// around a transport timeout must not hide that distinction.
		networkCause := cause
		var request *url.Error
		if errors.As(cause, &request) {
			networkCause = request.Err
		}
		var dns *net.DNSError
		var cert *tls.CertificateVerificationError
		var record tls.RecordHeaderError
		var unknown x509.UnknownAuthorityError
		var invalid x509.CertificateInvalidError
		var hostname x509.HostnameError
		var timeout net.Error
		switch {
		case errors.As(cause, &dns):
			code = "dns"
		case errors.As(cause, &cert), errors.As(cause, &record), errors.As(cause, &unknown), errors.As(cause, &invalid), errors.As(cause, &hostname):
			code = "tls"
		case errors.As(networkCause, &timeout) && timeout.Timeout():
			code = "transport-timeout"
		}
	}
	// Preserve explicit causes even if the caller context has a different error.
	if errors.Is(cause, context.Canceled) {
		return Failure(stage, "canceled", context.Canceled)
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return Failure(stage, "deadline", context.DeadlineExceeded)
	}
	// Transports may return their own error after observing request cancellation.
	if err := ctx.Err(); err != nil {
		return Failure(stage, "request-canceled", err)
	}
	return Failure(stage, code, cause)
}
