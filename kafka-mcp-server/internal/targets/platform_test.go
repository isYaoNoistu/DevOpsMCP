package targets

import "testing"

func TestPlatformCredentialsWithoutFiles(t *testing.T) {
	t.Setenv("KAFKA_TARGETS_JSON", `{"targets":[{"name":"test","brokers":["localhost:9092"],"topics":["*"],"groups":["*"],"sasl":{"mechanism":"PLAIN","username":"reader","password":"test-secret"}}]}`)
	all, err := New("/does/not/exist").List()
	if err != nil || len(all) != 1 {
		t.Fatalf("load: %v", err)
	}
	if all[0].SASL.Password != "test-secret" {
		t.Fatal("credential lost")
	}
}
func TestInvalidPlatformDoesNotFallBack(t *testing.T) {
	t.Setenv("KAFKA_TARGETS_JSON", " ")
	if _, err := New("/does/not/exist").List(); err == nil {
		t.Fatal("empty platform config accepted")
	}
}

func TestPlatformRejectsTLSFiles(t *testing.T) {
	t.Setenv("KAFKA_TARGETS_JSON", `{"targets":[{"name":"test","brokers":["localhost:9092"],"topics":["*"],"groups":["*"],"tls":{"enabled":true,"ca_file":"/no/file"}}]}`)
	if _, err := New("").List(); err == nil {
		t.Fatal("platform accepted certificate file")
	}
}
func TestInlineTLSValidation(t *testing.T) {
	base := Target{Name: "test", Brokers: []string{"localhost:9092"}, Topics: []string{"*"}, Groups: []string{"*"}}
	for _, tls := range []TLS{{Enabled: true, CertPEM: "cert"}, {Enabled: true, CAPEM: "ca", CAFile: "file"}, {CAPEM: "ca"}} {
		base.TLS = tls
		if validate(base) == nil {
			t.Fatal("invalid TLS accepted")
		}
	}
	base.TLS = TLS{Enabled: true, CAPEM: "ca", CertPEM: "cert", KeyPEM: "key"}
	if err := validate(base); err != nil {
		t.Fatal(err)
	}
}
