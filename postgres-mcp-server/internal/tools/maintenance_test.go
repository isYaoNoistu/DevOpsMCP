package tools

import (
	"strings"
	"testing"
)

func TestVacuumProgressSQL_PG16(t *testing.T) {
	sql := vacuumProgressSQL(160000)
	if !strings.Contains(sql, "max_dead_tuples") || !strings.Contains(sql, "num_dead_tuples") {
		t.Fatalf("pg16 columns missing: %s", sql)
	}
	if strings.Contains(sql, "max_dead_tuple_bytes") {
		t.Fatal("pg16 SQL should not use pg17 column names")
	}
}

func TestVacuumProgressSQL_PG17(t *testing.T) {
	sql := vacuumProgressSQL(170000)
	if !strings.Contains(sql, "max_dead_tuple_bytes") || !strings.Contains(sql, "dead_tuple_bytes") {
		t.Fatalf("pg17 columns missing: %s", sql)
	}
	if strings.Contains(sql, "max_dead_tuples") && !strings.Contains(sql, "max_dead_tuple_bytes") {
		t.Fatal("unexpected")
	}
	if strings.Contains(sql, "max_dead_tuples,") {
		t.Fatal("pg17 SQL should not use max_dead_tuples")
	}
}

func TestReplicationOverviewSQLHasStandbyFields(t *testing.T) {
	sql := replicationOverviewSQL()
	for _, want := range []string{
		"pg_is_in_recovery()",
		"pg_last_wal_receive_lsn()",
		"pg_last_wal_replay_lsn()",
		"pg_last_xact_replay_timestamp()",
		"pg_stat_wal_receiver",
	} {
		if want == "pg_stat_wal_receiver" {
			continue
		}
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %s in %s", want, sql)
		}
	}
	if strings.Contains(sql, "conninfo") {
		t.Fatal("wal receiver SQL must not select conninfo")
	}
}

func TestWalReceiverSQL(t *testing.T) {
	sql := walReceiverSQL()
	for _, want := range []string{"pg_stat_wal_receiver", "received_lsn", "sender_host"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(sql, "conninfo") {
		t.Fatal("conninfo may contain a password")
	}
}
