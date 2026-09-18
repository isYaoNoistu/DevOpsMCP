package tools

import (
	"strings"
	"testing"
)

func TestSplitRelation(t *testing.T) {
	s, r, err := splitRelation("orders")
	if err != nil || s != "" || r != "orders" {
		t.Fatalf("%q %q %v", s, r, err)
	}
	s, r, err = splitRelation("shop.orders")
	if err != nil || s != "shop" || r != "orders" {
		t.Fatalf("%q %q %v", s, r, err)
	}
	if _, _, err := splitRelation("a.b.c"); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := splitRelation("bad-name"); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadOnlyToolHints(t *testing.T) {
	tool := ReadOnlyTool("query_mysql", "Query MySQL", "read-only escape hatch")
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
		t.Fatal("ReadOnlyHint must be true")
	}
	if tool.OutputSchema == nil {
		t.Fatal("OutputSchema must be set")
	}
}

func TestSlowQuerySQL(t *testing.T) {
	sql := slowQuerySQL()
	for _, want := range []string{
		"events_statements_summary_by_digest",
		"AVG_TIMER_WAIT",
		"SUM_NO_INDEX_USED",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestInnodbLockWaitSQL(t *testing.T) {
	sql := innodbLockWaitSQL()
	for _, want := range []string{
		"data_lock_waits",
		"innodb_trx",
		"BLOCKING_ENGINE_TRANSACTION_ID",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestMetadataLockWaitSQL(t *testing.T) {
	sql := metadataLockWaitSQL()
	if !strings.Contains(sql, "metadata_locks") || !strings.Contains(sql, "PENDING") {
		t.Fatalf("unexpected %s", sql)
	}
}

func TestReplicationSQLOmitsPassword(t *testing.T) {
	sql := `
SELECT CHANNEL_NAME, HOST, PORT, USER, NETWORK_INTERFACE,
       AUTO_POSITION, SSL_ALLOWED, SSL_CA_FILE, HEARTBEAT_INTERVAL,
       COMPRESSION_ALGORITHM, GET_SOURCE_PUBLIC_KEY
FROM performance_schema.replication_connection_configuration
`
	if strings.Contains(strings.ToLower(sql), "password") {
		t.Fatal("replication configuration SQL must not select password")
	}
}

func TestCuratedSettingsCoverOpsKnobs(t *testing.T) {
	need := []string{"max_connections", "innodb_buffer_pool_size", "gtid_mode", "read_only", "long_query_time"}
	have := map[string]struct{}{}
	for _, n := range curatedSettings {
		have[n] = struct{}{}
	}
	for _, n := range need {
		if _, ok := have[n]; !ok {
			t.Fatalf("missing curated setting %s", n)
		}
	}
}

func TestErrorLogSQLFilter(t *testing.T) {
	if !strings.Contains(errorLogSQL("deadlock"), "DATA LIKE") {
		t.Fatal("query filter missing")
	}
	if strings.Contains(errorLogSQL(""), "DATA LIKE") {
		t.Fatal("empty query should not add LIKE")
	}
}
