package broker

import (
	"encoding/json"
	"errors"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"strings"
	"testing"
)

func TestConfigRedaction(t *testing.T) {
	secret := "credential-must-not-leak"
	c := kmsg.DescribeConfigsResponseResourceConfig{Name: "sasl.jaas.config", Value: &secret, ConfigSynonyms: []kmsg.DescribeConfigsResponseResourceConfigConfigSynonym{{Name: "sasl.jaas.config", Value: &secret}}}
	b, _ := json.Marshal(configView(c, true))
	if strings.Contains(string(b), secret) {
		t.Fatal(string(b))
	}
	c.Name = "innocent"
	c.IsSensitive = true
	b, _ = json.Marshal(configView(c, true))
	if strings.Contains(string(b), secret) {
		t.Fatal(string(b))
	}
}
func TestRecordBudgetAndHiddenPayload(t *testing.T) {
	r := &kgo.Record{Key: []byte("secret-key"), Value: []byte("secret-value"), Headers: []kgo.RecordHeader{{Key: "secret-header", Value: []byte("secret")}}}
	v, _, ok := recordView(r, false, 65536)
	if !ok {
		t.Fatal("record rejected")
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "secret") {
		t.Fatal(string(b))
	}
	r.Value = make([]byte, 17000)
	v, _, ok = recordView(r, true, 65536)
	if !ok || v["payload_omitted"] != true {
		t.Fatalf("oversized record: %v", v)
	}
	_, _, ok = recordView(&kgo.Record{Value: make([]byte, 1000)}, true, 100)
	if ok {
		t.Fatal("budget exceeded")
	}
}
func TestSafeErrors(t *testing.T) {
	if strings.Contains(safeError(errors.New("password=secret")), "secret") {
		t.Fatal("leaked error")
	}
}
