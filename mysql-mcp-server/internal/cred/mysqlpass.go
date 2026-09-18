package cred

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

func lookupMysqlpass(host string, port int, dbname, user string) (string, error) {
	path := mysqlpassPath()
	if path == "" {
		return "", nil
	}
	if err := checkMysqlpassPerms(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := splitPass(line)
		if len(fields) < 5 {
			continue
		}
		if !fieldMatch(fields[0], host) {
			continue
		}
		if !fieldMatch(fields[1], strconv.Itoa(port)) {
			continue
		}
		if !fieldMatch(fields[2], dbname) {
			continue
		}
		if !fieldMatch(fields[3], user) {
			continue
		}
		return fields[4], nil
	}
	return "", sc.Err()
}

func checkMysqlpassPerms(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("mysqlpass file %s must be mode 0600 or stricter (got %o)", path, st.Mode().Perm())
	}
	return nil
}

func fieldMatch(pat, got string) bool {
	return pat == "*" || pat == got
}

func splitPass(line string) []string {
	var fields []string
	var cur strings.Builder
	esc := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if esc {
			cur.WriteByte(c)
			esc = false
			continue
		}
		if c == '\\' {
			esc = true
			continue
		}
		if c == ':' {
			fields = append(fields, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	fields = append(fields, cur.String())
	return fields
}

func mysqlpassPath() string {
	if p := os.Getenv("MYSQL_PASSFILE"); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		app := os.Getenv("APPDATA")
		if app == "" {
			return ""
		}
		return filepath.Join(app, "mysql", "mysqlpass.conf")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mysqlpass")
}
