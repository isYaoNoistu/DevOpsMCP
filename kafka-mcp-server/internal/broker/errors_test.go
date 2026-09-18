package broker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
)

func TestErrorDetailsClassificationAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		err                   error
		code, category, stage string
		retry                 bool
	}{
		{"unknown", errors.New("password=secret host=private.example.com"), "kafka_request_failed", "unknown", "request", false},
		{"dns", &net.OpError{Op: "dial", Err: &net.DNSError{Name: "private.example.com", Err: "password=secret", IsNotFound: true}}, "dns_lookup_failed", "transport", "dns", false},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, "connection_refused", "transport", "connect", true},
		{"dial-timeout", &net.OpError{Op: "dial", Err: context.DeadlineExceeded}, "timeout", "timeout", "connect", true},
		{"request-timeout", context.DeadlineExceeded, "timeout", "timeout", "request", true},
		{"tls-certificate", &tls.CertificateVerificationError{Err: x509.HostnameError{Host: "private.example.com"}}, "tls_failed", "tls", "tls", false},
		{"tls-alert", tls.AlertError(40), "tls_failed", "tls", "tls", false},
		{"authz", kerr.TopicAuthorizationFailed, "TOPIC_AUTHORIZATION_FAILED", "authorization", "request", false},
		{"authn", kerr.SaslAuthenticationFailed, "SASL_AUTHENTICATION_FAILED", "authentication", "request", false},
		{"retryable-broker", kerr.NotLeaderForPartition, "NOT_LEADER_FOR_PARTITION", "broker", "request", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ErrorDetails(fmt.Errorf("password=secret host=private.example.com: %w", tc.err))
			if got["error_code"] != tc.code || got["category"] != tc.category || got["stage"] != tc.stage || got["retryable"] != tc.retry {
				t.Fatalf("details: %#v", got)
			}
			b, _ := json.Marshal(got)
			if strings.Contains(string(b), "secret") || strings.Contains(string(b), "private.example.com") {
				t.Fatalf("unsafe: %s", b)
			}
		})
	}
}
