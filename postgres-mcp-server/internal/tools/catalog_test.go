package tools

import (
	"strings"
	"testing"
)

func TestSplitRelation(t *testing.T) {
	s, r, err := splitRelation("holiday")
	if err != nil || s != "" || r != "holiday" {
		t.Fatalf("%q %q %v", s, r, err)
	}
	s, r, err = splitRelation("power_trade.holiday")
	if err != nil || s != "power_trade" || r != "holiday" {
		t.Fatalf("%q %q %v", s, r, err)
	}
	if _, _, err := splitRelation("a.b.c"); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := splitRelation("bad-name"); err == nil {
		t.Fatal("expected error")
	}
}

func TestReplicaIdentityExpr(t *testing.T) {
	for _, want := range []string{
		"WHEN 'd' THEN 'default'",
		"WHEN 'n' THEN 'nothing'",
		"WHEN 'f' THEN 'full'",
		"WHEN 'i' THEN 'index'",
		"::text",
	} {
		if !strings.Contains(replicaIdentityExpr, want) {
			t.Fatalf("missing %s in %s", want, replicaIdentityExpr)
		}
	}
}
