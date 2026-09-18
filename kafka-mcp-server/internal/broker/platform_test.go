package broker

import (
	"crypto/x509"
	"encoding/pem"
	"kafka-mcp-server/internal/targets"
	"net/http/httptest"
	"testing"
)

func TestInlineTLSCredentials(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	pair := server.TLS.Certificates[0]
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}))
	target := targets.Target{Brokers: []string{"localhost:9093"}, TLS: targets.TLS{Enabled: true, CAPEM: cert, CertPEM: cert, KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))}}
	client, err := New(target)
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	target.TLS.KeyPEM = "SECRET_BAD_KEY"
	if _, err = New(target); err == nil || err.Error() != "TLS client certificate unavailable" {
		t.Fatal("unsafe TLS error")
	}
}
