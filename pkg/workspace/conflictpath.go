package workspace

import (
	"strings"
	"unicode/utf8"
)

// conflictMarker separates the base name from the conflict identity:
// name.saker-conflict-<device8>-<request8>-<ext> (doc §7).
const conflictMarker = ".saker-conflict-"

// ConflictPath derives the deterministic conflict path for a put operation
// whose base revision did not match. The result depends only on the input
// path, device ID and request ID — never on time or randomness — so
// retries of the same request address the same conflict path (doc §7,
// CF-01/CF-02).
//
// The derived path always satisfies ValidatePath with the given limits:
// device/request identifiers are reduced to 8 alphanumeric characters and
// the base name is truncated (extension and conflict marker preserved)
// when the segment or total path limit would otherwise be exceeded.
func ConflictPath(p, deviceID, requestID string, limits Limits) string {
	maxPath := limits.MaxPathBytes
	if maxPath <= 0 {
		maxPath = MaxPathBytesDefault
	}
	maxSegment := limits.MaxPathSegmentBytes
	if maxSegment <= 0 {
		maxSegment = MaxPathSegmentBytesDefault
	}

	dir, name := splitDirBase(p)
	ext := pathExt(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem = "file"
	}

	// Fixed overhead inside one segment: marker + device8 + "-" + request8.
	suffix := conflictMarker + token8(deviceID) + "-" + token8(requestID) + ext
	segmentBudget := maxSegment - len(suffix)
	stem = truncateUTF8(stem, segmentBudget)

	conflict := stem + suffix
	dirBudget := maxPath - len(conflict)
	if dir != "" {
		// One byte for the joining slash.
		dirBudget--
	}
	if dirBudget < 0 {
		// Extremely small total path budgets: shrink the stem further so
		// the whole path still fits. Deterministic, input-only.
		conflict = truncateUTF8(conflict, maxPath)
		if dir != "" {
			dir = ""
			conflict = truncateUTF8(conflict, maxPath)
		}
	}
	if dir == "" {
		return conflict
	}
	return dir + "/" + conflict
}

// token8 reduces an identifier to its first 8 characters, mapping every
// character outside [A-Za-z0-9] to '-' so conflict paths stay valid on all
// supported file systems and pass ValidatePath. Shorter identifiers are
// padded with '-' to keep the marker shape stable.
func token8(id string) string {
	var b strings.Builder
	for _, r := range id {
		if b.Len() >= 8 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	for b.Len() < 8 {
		b.WriteByte('-')
	}
	return b.String()
}

// splitDirBase splits a validated path into its directory part (without
// trailing slash) and base name.
func splitDirBase(p string) (dir, base string) {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

// pathExt returns the extension of base including the dot, or "". A
// leading dot does not start an extension (".env" has none).
func pathExt(base string) string {
	i := strings.LastIndexByte(base, '.')
	if i <= 0 || i == len(base)-1 {
		return ""
	}
	return base[i:]
}

// truncateUTF8 shortens s to at most n bytes without splitting runes and
// without leaving forbidden trailing characters (space or dot) that would
// fail segment validation.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return "x"
	}
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	for len(cut) > 0 && (cut[len(cut)-1] == ' ' || cut[len(cut)-1] == '.') {
		cut = cut[:len(cut)-1]
	}
	if cut == "" {
		return "x"
	}
	return cut
}
