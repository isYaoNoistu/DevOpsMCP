package quote

import "testing"

func TestPOSIX(t *testing.T) {
	cases := map[string]string{
		"":          "''",
		"abc":       "'abc'",
		"a b":       "'a b'",
		"it's":      `'it'"'"'s'`,
		"ERROR|EOF": "'ERROR|EOF'",
		`$HOME; rm`: `'$HOME; rm'`,
	}
	for in, want := range cases {
		if got := POSIX(in); got != want {
			t.Errorf("POSIX(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCheckRemoteArg(t *testing.T) {
	if err := CheckRemoteArg("p", "ok", 10); err != nil {
		t.Fatal(err)
	}
	if err := CheckRemoteArg("p", "toolongvalue", 4); err == nil {
		t.Fatal("expected length error")
	}
	if err := CheckRemoteArg("p", "a\x00b", 10); err == nil {
		t.Fatal("expected NUL error")
	}
}
