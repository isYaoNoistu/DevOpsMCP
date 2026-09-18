package limits

const (
	MaxBytes       = 256 * 1024
	DefaultLines   = 80
	MaxLines       = 200
	MaxPattern     = 256
	MaxPath        = 1024
	DefaultTimeout = "20s"
	ListMax        = 200
	MaxDepth       = 4
	MaxContext     = 5
)

func ClampLines(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
