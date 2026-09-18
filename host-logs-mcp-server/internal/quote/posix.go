package quote

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// POSIX wraps s for a POSIX remote shell (single quotes). Empty string becomes ''.
func POSIX(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// CheckRemoteArg rejects NUL and non-UTF-8 so values can be ssh argv / quoted safely.
func CheckRemoteArg(name, s string, max int) error {
	if strings.IndexByte(s, 0) >= 0 {
		return fmt.Errorf("%s contains a NUL byte", name)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("%s is not valid UTF-8", name)
	}
	if max > 0 && len(s) > max {
		return fmt.Errorf("%s is too long (max %d bytes)", name, max)
	}
	return nil
}
