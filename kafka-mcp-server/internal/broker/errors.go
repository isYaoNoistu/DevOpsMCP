package broker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"

	"github.com/twmb/franz-go/pkg/kerr"
)

// ScopeLimitError means the bounded query cannot represent the requested scope.
type ScopeLimitError struct{}

func (*ScopeLimitError) Error() string { return "scope_limit_exceeded" }

// ErrorDetails returns only fixed diagnostic labels and Kafka numeric codes.
// It never includes error text, addresses, certificate names or credentials.
func ErrorDetails(err error) map[string]any {
	code, category, stage, retryable := "kafka_request_failed", "unknown", "request", false
	var scope *ScopeLimitError
	var dns *net.DNSError
	var op *net.OpError
	var ke *kerr.Error
	var cert *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case err == nil:
		code, category = "ok", "none"
	case errors.As(err, &scope):
		code, category = "scope_limit_exceeded", "scope"
	case errors.Is(err, context.Canceled):
		code, category = "cancelled", "cancellation"
	case errors.As(err, &dns):
		code, category, stage, retryable = "dns_lookup_failed", "transport", "dns", dns.IsTemporary || dns.IsTimeout
	case errors.As(err, &cert) || errors.As(err, &record) || errors.As(err, &alert) || errors.As(err, &authority) || errors.As(err, &hostname) || errors.As(err, &invalid):
		code, category, stage = "tls_failed", "tls", "tls"
	case errors.Is(err, syscall.ECONNREFUSED):
		code, category, stage, retryable = "connection_refused", "transport", "connect", true
	case errors.Is(err, context.DeadlineExceeded):
		code, category, retryable = "timeout", "timeout", true
		if errors.As(err, &op) && op.Op == "dial" {
			stage = "connect"
		}
	case errors.As(err, &ke):
		known := kerr.TypedErrorForCode(ke.Code)
		if known != nil {
			code, category, retryable = known.Message, "broker", known.Retriable
			switch known.Code {
			case 29, 30, 31, 53, 65:
				category = "authorization"
			case 33, 34, 58:
				category = "authentication"
			}
		}
	case errors.As(err, &op):
		category = "transport"
		if op.Op == "dial" {
			stage = "connect"
		}
		if op.Timeout() {
			code, category, retryable = "timeout", "timeout", true
		}
	}
	out := map[string]any{"error_code": code, "category": category, "stage": stage, "retryable": retryable}
	if ke != nil {
		out["kafka_error_code"] = ke.Code
	}
	return out
}
