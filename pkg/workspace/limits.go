package workspace

// Limits carries the hard request limits of doc §8.3. Configuration may
// lower every value but never raise it above the documented defaults.
type Limits struct {
	// MaxCommitBodyBytes caps one commit request body (default 2 MiB).
	MaxCommitBodyBytes int64
	// MaxCommitOperations caps operations per commit (default 1000).
	MaxCommitOperations int
	// MaxPathBytes caps total path length (default 4096).
	MaxPathBytes int
	// MaxPathSegmentBytes caps one path segment (default 255).
	MaxPathSegmentBytes int
	// MaxNoteBytes caps commit and restore notes (default 1024).
	MaxNoteBytes int
	// MaxReadEventBatch caps read events per request (default 1000).
	MaxReadEventBatch int
}

// Hard limit defaults from doc §8.3.
const (
	DefaultMaxCommitBodyBytes  int64 = 2 * 1024 * 1024
	DefaultMaxCommitOperations       = 1000
	DefaultMaxNoteBytes              = 1024
	DefaultMaxReadEventBatch         = 1000
)

// Pagination defaults shared by all workspace list endpoints (doc §8.2).
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// DefaultLimits returns the documented default hard limits.
func DefaultLimits() Limits {
	return Limits{
		MaxCommitBodyBytes:  DefaultMaxCommitBodyBytes,
		MaxCommitOperations: DefaultMaxCommitOperations,
		MaxPathBytes:        MaxPathBytesDefault,
		MaxPathSegmentBytes: MaxPathSegmentBytesDefault,
		MaxNoteBytes:        DefaultMaxNoteBytes,
		MaxReadEventBatch:   DefaultMaxReadEventBatch,
	}
}

// ClampLimits returns l with every unset value filled from the defaults
// and every value capped at its default, so configuration can lower but
// never raise the hard limits.
func ClampLimits(l Limits) Limits {
	def := DefaultLimits()
	clampInt := func(v, def int) int {
		if v <= 0 || v > def {
			return def
		}
		return v
	}
	out := Limits{
		MaxCommitBodyBytes:  l.MaxCommitBodyBytes,
		MaxCommitOperations: clampInt(l.MaxCommitOperations, def.MaxCommitOperations),
		MaxPathBytes:        clampInt(l.MaxPathBytes, def.MaxPathBytes),
		MaxPathSegmentBytes: clampInt(l.MaxPathSegmentBytes, def.MaxPathSegmentBytes),
		MaxNoteBytes:        clampInt(l.MaxNoteBytes, def.MaxNoteBytes),
		MaxReadEventBatch:   clampInt(l.MaxReadEventBatch, def.MaxReadEventBatch),
	}
	if out.MaxCommitBodyBytes <= 0 || out.MaxCommitBodyBytes > def.MaxCommitBodyBytes {
		out.MaxCommitBodyBytes = def.MaxCommitBodyBytes
	}
	return out
}

// ClampListLimit normalizes a client-supplied page limit to the documented
// range: default 100, maximum 500.
func ClampListLimit(n int) int {
	if n <= 0 {
		return DefaultListLimit
	}
	if n > MaxListLimit {
		return MaxListLimit
	}
	return n
}
