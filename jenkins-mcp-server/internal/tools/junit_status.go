package tools

// JUnitState collapses Jenkins' per-case status strings into the four
// states that every test-aware tool here cares about. StateUnknown is the
// fallback for plugin-specific statuses we don't model, and doubles as
// the "absent from this build" sentinel callers can map to.
type JUnitState int

// The four states every test-aware tool models. StateUnknown is also
// the "absent" sentinel for plugin statuses we don't recognize and for
// callers using map-miss semantics.
const (
	StateUnknown JUnitState = iota
	StatePass
	StateFail
	StateSkip
)

// String returns the short label used in human-facing diff output.
// StateUnknown renders as the empty string so callers can use it as an
// "ignore this case" sentinel in the same idiom as a nil map miss.
func (s JUnitState) String() string {
	switch s {
	case StatePass:
		return "PASS"
	case StateFail:
		return "FAIL"
	case StateSkip:
		return "SKIP"
	default:
		return ""
	}
}

// NormalizeJUnitStatus maps Jenkins' per-case status enum to JUnitState.
// PASSED/FIXED collapse to StatePass; FAILED/REGRESSION to StateFail;
// SKIPPED to StateSkip; everything else (or empty) to StateUnknown.
func NormalizeJUnitStatus(s string) JUnitState {
	switch s {
	case "PASSED", "FIXED":
		return StatePass
	case "FAILED", "REGRESSION":
		return StateFail
	case "SKIPPED":
		return StateSkip
	default:
		return StateUnknown
	}
}
