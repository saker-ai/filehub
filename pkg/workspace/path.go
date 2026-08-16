package workspace

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

// Path limits enforced by validation. They mirror the server defaults and
// are shared with the conflict path derivation so conflict paths always
// satisfy validation.
const (
	// MaxPathBytesDefault is the default maximum total length of a path.
	MaxPathBytesDefault = 4096
	// MaxPathSegmentBytesDefault is the default maximum length of one path
	// segment.
	MaxPathSegmentBytesDefault = 255
)

// windowsReservedNames are DOS device names that must never appear as a
// segment base name, case-insensitively and with or without an extension
// (SEC-01, doc §6.1).
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// excludedDirs are directory names (matched at any depth) whose content is
// never synchronized. The list is intentionally conservative and explicit:
// version control internals, the sync engine's own state, and agent
// settings/hook directories whose sync could leak credentials or let a
// remote peer install agent configuration.
var excludedDirs = map[string]bool{
	".git":        true, // version control internals
	".saker-sync": true, // local sync state (doc §6.2)
	".claude":     true, // Claude Code settings/hooks
	".saker":      true, // Saker agent settings/hooks
	".codex":      true, // Codex CLI settings
	".gemini":     true, // Gemini CLI settings
	".cursor":     true, // Cursor agent settings
}

// excludedFiles are exact base names that are never synchronized.
var excludedFiles = map[string]bool{
	".env":          true, // credentials
	".mcp.json":     true, // MCP server configuration with possible secrets
	".sakerignore":  true, // local-only policy, doc §6.1 / R-05
	".DS_Store":     true, // macOS temp metadata
	".sakerignore~": true,
}

// ValidatePath checks one slash-separated relative path against the shared
// rules of doc §6.1. limits may lower the default byte caps; values <= 0
// fall back to the defaults.
//
// Rules:
//   - UTF-8, slash-separated, relative; no empty paths.
//   - No absolute paths, no `.`/`..` segments, no NUL, no C0/C1 control
//     characters, no bidirectional control characters, no zero-width or
//     invisible format characters, no backslashes.
//   - The input must already equal its cleaned form; ambiguous spellings
//     such as `a//b`, `a/./b` or trailing slashes are rejected instead of
//     being normalized.
//   - Segment and total byte length are capped.
//   - Windows reserved names are rejected case-insensitively, with or
//     without an extension.
//   - Segments must not end with a space or a dot.
func ValidatePath(p string, limits Limits) error {
	maxPath := limits.MaxPathBytes
	if maxPath <= 0 {
		maxPath = MaxPathBytesDefault
	}
	maxSegment := limits.MaxPathSegmentBytes
	if maxSegment <= 0 {
		maxSegment = MaxPathSegmentBytesDefault
	}

	if p == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if !utf8.ValidString(p) {
		return fmt.Errorf("%w: not valid UTF-8", ErrInvalidPath)
	}
	if len(p) > maxPath {
		return fmt.Errorf("%w: path exceeds %d bytes", ErrInvalidPath, maxPath)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: absolute path", ErrInvalidPath)
	}
	if strings.Contains(p, "\\") {
		return fmt.Errorf("%w: backslash not allowed", ErrInvalidPath)
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("%w: NUL byte", ErrInvalidPath)
	}
	// The cleaned form must be identical: rejects "//", "/./", "/../",
	// trailing "/" and embedded "."/".." segments.
	if path.Clean(p) != p {
		return fmt.Errorf("%w: path is not in clean form", ErrInvalidPath)
	}
	for _, r := range p {
		if isForbiddenRune(r) {
			return fmt.Errorf("%w: forbidden control or invisible character U+%04X", ErrInvalidPath, r)
		}
	}
	for _, segment := range strings.Split(p, "/") {
		if err := validateSegment(segment, maxSegment); err != nil {
			return err
		}
	}
	return nil
}

func validateSegment(segment string, maxSegment int) error {
	if segment == "" {
		return fmt.Errorf("%w: empty segment", ErrInvalidPath)
	}
	if len(segment) > maxSegment {
		return fmt.Errorf("%w: segment exceeds %d bytes", ErrInvalidPath, maxSegment)
	}
	if segment == "." || segment == ".." {
		return fmt.Errorf("%w: dot segment", ErrInvalidPath)
	}
	if strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") {
		return fmt.Errorf("%w: segment ends with space or dot", ErrInvalidPath)
	}
	base := segment
	if i := strings.IndexByte(segment, '.'); i >= 0 {
		base = segment[:i]
	}
	if windowsReservedNames[strings.ToUpper(base)] {
		return fmt.Errorf("%w: reserved Windows name %q", ErrInvalidPath, segment)
	}
	return nil
}

// isForbiddenRune reports whether r is a control, bidirectional control or
// invisible format character that must never appear in a synced path.
func isForbiddenRune(r rune) bool {
	switch {
	case r <= 0x1F: // C0 control characters and NUL
		return true
	case r == 0x7F: // DEL
		return true
	case r >= 0x80 && r <= 0x9F: // C1 control characters
		return true
	case r >= 0x202A && r <= 0x202E: // bidi overrides LRE..RLO and PDF
		return true
	case r >= 0x2066 && r <= 0x2069: // bidi isolates LRI..PDI
		return true
	case r >= 0x200B && r <= 0x200F: // zero-width and LRM/RLM
		return true
	case r == 0x2060: // word joiner
		return true
	case r == 0xFEFF: // zero-width no-break space / BOM
		return true
	}
	return false
}

// IsExcluded reports whether a path must never be uploaded to or
// materialized from a workspace (doc §6.1, SEC-02). The built-in rules
// cannot be overridden by client-side ignore files.
func IsExcluded(p string) bool {
	if p == "" || !utf8.ValidString(p) {
		return true
	}
	segments := strings.Split(p, "/")
	for _, segment := range segments {
		if excludedDirs[segment] {
			return true
		}
	}
	base := segments[len(segments)-1]
	if excludedFiles[base] {
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		return true
	}
	if strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, "~") {
		return true
	}
	return false
}

// ExcludedReason returns a short human-readable reason when IsExcluded is
// true; the empty string otherwise. Useful for error messages and tests.
func ExcludedReason(p string) string {
	if p == "" || !utf8.ValidString(p) {
		return "invalid path"
	}
	segments := strings.Split(p, "/")
	for _, segment := range segments {
		if excludedDirs[segment] {
			return "directory " + segment + " is excluded from sync"
		}
	}
	base := segments[len(segments)-1]
	switch {
	case excludedFiles[base]:
		return "file " + base + " is excluded from sync"
	case strings.HasPrefix(base, ".env."):
		return "dotenv files are excluded from sync"
	case strings.HasSuffix(base, ".tmp"), strings.HasSuffix(base, "~"):
		return "temporary files are excluded from sync"
	}
	return ""
}
