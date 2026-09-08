package sqlguard

import "testing"

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

func TestCheckReadQuery_RejectsCommentHiddenInsert(t *testing.T) {
	if _, err := CheckReadQuery("SELECT 1; --\nINSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsWithInsert(t *testing.T) {
	if _, err := CheckReadQuery("WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsPgReadFile(t *testing.T) {
	if _, err := CheckReadQuery("SELECT pg_read_file('/etc/passwd')"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckReadQuery_RejectsCopy(t *testing.T) {
	if _, err := CheckReadQuery("COPY t TO STDOUT"); err == nil {
		t.Fatal("expected error")
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

func TestCheckReadQuery_RejectsQuotedDblinkExec(t *testing.T) {
	if _, err := CheckReadQuery(`SELECT public."dblink_exec"('dbname=x','VACUUM t')`); err == nil {
		t.Fatal("expected quoted dblink_exec to be rejected")
	}
}

func TestCheckReadQuery_RejectsQuotedCatalogFunc(t *testing.T) {
	if _, err := CheckReadQuery(`SELECT pg_catalog."pg_advisory_lock"(1)`); err == nil {
		t.Fatal("expected quoted advisory lock to be rejected")
	}
}

func TestCheckReadQuery_RejectsAdvisoryLock(t *testing.T) {
	if _, err := CheckReadQuery("SELECT pg_advisory_lock(1)"); err == nil {
		t.Fatal("expected advisory lock to be rejected")
	}
}

func TestCheckReadQuery_RejectsDblinkConnectU(t *testing.T) {
	if _, err := CheckReadQuery("SELECT dblink_connect_u('h')"); err == nil {
		t.Fatal("expected dblink_connect_u to be rejected")
	}
}

func TestCheckReadQuery_AllowsQuotedKeywordColumn(t *testing.T) {
	if _, err := CheckReadQuery(`SELECT "lock", "comment" FROM t`); err != nil {
		t.Fatalf("quoted keyword columns should be allowed: %v", err)
	}
}

func TestCheckReadQuery_AllowsDollarQuotedCommentLookalike(t *testing.T) {
	if _, err := CheckReadQuery("SELECT $tag$ -- INSERT INTO t $tag$"); err != nil {
		t.Fatalf("dollar-quoted -- should not be stripped as a comment: %v", err)
	}
}

func TestCheckReadQuery_RejectsCommentHiddenByDollarCloser(t *testing.T) {
	if _, err := CheckReadQuery("SELECT $tag$foo -- $tag$; SELECT pg_read_file('x')"); err == nil {
		t.Fatal("closing dollar tag after -- must not be stripped as a comment")
	}
}

func TestCheckReadQuery_RejectsBlockCommentHiddenByDollarCloser(t *testing.T) {
	if _, err := CheckReadQuery("SELECT $tag$ /* $tag$ */ || pg_read_file('x')"); err == nil {
		t.Fatal("closing dollar tag inside /* */ must not be stripped as a comment")
	}
}

func TestCheckReadQuery_RejectsUnclosedDollarThenSecondStatement(t *testing.T) {
	if _, err := CheckReadQuery("SELECT 1; INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("expected multi-statement reject")
	}
}
