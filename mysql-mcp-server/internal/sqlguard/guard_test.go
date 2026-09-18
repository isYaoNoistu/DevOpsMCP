package sqlguard

import "testing"

func TestReadQueryPreservesQuotedSQL(t *testing.T) {
	for _, sql := range []string{
		`SELECT 'it''s' AS value`,
		`SELECT 'a'' OR ''b' AS value`,
		`SELECT 'C:\logs\new' AS value`,
		`SELECT 'it\'s # text' AS value`,
		`SELECT "a""b" AS value`,
		"SELECT `a``b` FROM t",
	} {
		got, err := CheckReadQuery(sql)
		if err != nil || got != sql {
			t.Errorf("query %q became %q: %v", sql, got, err)
		}
	}
	got, err := CheckReadQuery("/* before */ SELECT 'it''s' /* after */ # end")
	if err != nil || got != "SELECT 'it''s'" {
		t.Fatalf("comment removal changed literal: %q, %v", got, err)
	}
}

func TestReadQueryRejectsWriteAfterEscapedLiteral(t *testing.T) {
	if _, err := CheckReadQuery(`SELECT 'it\'s'; DELETE FROM t`); err == nil {
		t.Fatal("escaped string must not hide a second statement")
	}
}

func TestCheckReadQuery_AllowsSelect(t *testing.T) {
	got, err := CheckReadQuery("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "SELECT 1" {
		t.Fatalf("got %q", got)
	}
}

func TestCheckReadQuery_AllowsSelectInsertLiteral(t *testing.T) {
	if _, err := CheckReadQuery("SELECT 'INSERT' AS x"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckReadQuery_RejectsInsert(t *testing.T) {
	if _, err := CheckReadQuery("INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsMultiStatement(t *testing.T) {
	if _, err := CheckReadQuery("SELECT 1; SELECT 2"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsExplainAnalyze(t *testing.T) {
	if _, err := CheckReadQuery("EXPLAIN ANALYZE SELECT 1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsHashCommentHiddenInsert(t *testing.T) {
	if _, err := CheckReadQuery("SELECT 1; #\nINSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsUse(t *testing.T) {
	if _, err := CheckReadQuery("USE mysql"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsGetLock(t *testing.T) {
	if _, err := CheckReadQuery("SELECT GET_LOCK('x', 1)"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsBacktickGetLock(t *testing.T) {
	if _, err := CheckReadQuery("SELECT `GET_LOCK`('x', 1)"); err == nil {
		t.Fatal("expected quoted GET_LOCK to be rejected")
	}
}

func TestCheckReadQuery_RejectsLoadFile(t *testing.T) {
	if _, err := CheckReadQuery("SELECT LOAD_FILE('/etc/passwd')"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsSleep(t *testing.T) {
	if _, err := CheckReadQuery("SELECT SLEEP(10)"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsForUpdate(t *testing.T) {
	if _, err := CheckReadQuery("SELECT id FROM t FOR UPDATE"); err == nil {
		t.Fatal("expected FOR UPDATE to be rejected")
	}
}

func TestCheckReadQuery_RejectsForShare(t *testing.T) {
	if _, err := CheckReadQuery("SELECT id FROM t FOR SHARE"); err == nil {
		t.Fatal("expected FOR SHARE to be rejected")
	}
}

func TestCheckReadQuery_RejectsIntoOutfile(t *testing.T) {
	if _, err := CheckReadQuery("SELECT 1 INTO OUTFILE '/tmp/x'"); err == nil {
		t.Fatal("expected INTO to be rejected")
	}
}

func TestCheckReadQuery_RejectsVersionedComment(t *testing.T) {
	if _, err := CheckReadQuery("SELECT 1 /*!50000 FOR UPDATE */"); err == nil {
		t.Fatal("expected versioned comment to be rejected")
	}
}

func TestCheckReadQuery_AllowsBacktickKeywordColumn(t *testing.T) {
	if _, err := CheckReadQuery("SELECT `lock`, `comment` FROM t"); err != nil {
		t.Fatalf("quoted keyword columns should be allowed: %v", err)
	}
}

func TestCheckReadQuery_AllowsHashInsideString(t *testing.T) {
	if _, err := CheckReadQuery("SELECT '# INSERT INTO t'"); err != nil {
		t.Fatalf("hash inside string should be allowed: %v", err)
	}
}

func TestClampRows(t *testing.T) {
	if ClampRows(0) != DefaultRows {
		t.Fatal("default")
	}
	if ClampRows(9999) != MaxRows {
		t.Fatal("cap")
	}
}
