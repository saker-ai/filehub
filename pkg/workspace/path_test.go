package workspace

import (
	"strings"
	"testing"
)

func TestValidatePathAcceptsLegalPaths(t *testing.T) {
	valid := []string{
		"report.md",
		"shared/report.md",
		"a/b/c/d.txt",
		"docs/2026-08/notes.md",
		"文件/报告.md",
		"naïve-café/résumé.pdf",
		"file.with.dots.txt",
		"UPPERCASE.TXT",
		"a-b_c d/e",
		strings.Repeat("a", 255),
		strings.Repeat("b/", 400) + "end", // long but under 4096 bytes
	}
	for _, p := range valid {
		if err := ValidatePath(p, Limits{}); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", p, err)
		}
	}
}

func TestValidatePathRejectsIllegalPaths(t *testing.T) {
	longSegment := strings.Repeat("a", 256)
	tooLong := strings.Repeat("x/", 2048) + "end" // > 4096 bytes
	cases := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"absolute", "/abs/path"},
		{"absolute root", "/"},
		{"dot segment", "a/./b"},
		{"dotdot segment", "a/../b"},
		{"dotdot leading", "../escape"},
		{"backslash", "a\\b"},
		{"nul byte", "a\x00b"},
		{"c0 control", "a\x01b"},
		{"tab", "a\tb"},
		{"newline", "a\nb"},
		{"del", "a\x7fb"},
		{"c1 control", "ab"},
		{"bidi rlo", "a‮b"},
		{"bidi lre", "a‪b"},
		{"bidi pdf", "a‮b"},
		{"bidi isolate lri", "a⁦b"},
		{"bidi isolate pdi", "a⁩b"},
		{"zero width space", "a​b"},
		{"zero width joiner", "a‍b"},
		{"ltr mark", "a‎b"},
		{"word joiner", "a⁠b"},
		{"bom", "a\uFEFFb"},
		{"double slash", "a//b"},
		{"trailing slash", "a/b/"},
		{"dot path", "."},
		{"dotdot path", ".."},
		{"segment too long", longSegment},
		{"segment too long nested", "dir/" + longSegment},
		{"path too long", tooLong},
		{"windows reserved con", "CON"},
		{"windows reserved con lower", "con"},
		{"windows reserved con with ext", "con.txt"},
		{"windows reserved nested", "dir/NUL.md"},
		{"windows reserved com1", "COM1"},
		{"windows reserved lpt9 ext", "lpt9.txt"},
		{"windows reserved aux", "aux.log"},
		{"trailing space", "a/b "},
		{"trailing dot", "a/b."},
		{"segment trailing space", "a /b"},
		{"invalid utf8", "a/\xff\xfeb"},
	}
	for _, tc := range cases {
		if err := ValidatePath(tc.path, Limits{}); err == nil {
			t.Errorf("%s: ValidatePath(%q) = nil, want error", tc.name, tc.path)
		}
	}
}

func TestValidatePathHonorsLoweredLimits(t *testing.T) {
	limits := Limits{MaxPathBytes: 16, MaxPathSegmentBytes: 4}
	if err := ValidatePath("abcde", limits); err == nil {
		t.Fatal("segment over lowered limit accepted")
	}
	if err := ValidatePath("abcd/abcd/abcd/abcd", limits); err == nil {
		t.Fatal("path over lowered limit accepted")
	}
	if err := ValidatePath("abcd/abcd", limits); err != nil {
		t.Fatalf("path within lowered limits rejected: %v", err)
	}
}

func TestValidatePathRejectsUnnormalizedEquivalents(t *testing.T) {
	// Ambiguous spellings must be rejected outright, never cleaned.
	for _, p := range []string{"a/./b", "a//b", "a/b/../c", "./a", "a/"} {
		if err := ValidatePath(p, Limits{}); err == nil {
			t.Errorf("ValidatePath(%q) accepted non-clean spelling", p)
		}
	}
}

func TestIsExcluded(t *testing.T) {
	excluded := []string{
		".git/config",
		".git",
		"repo/.git/HEAD",
		".saker-sync/state.json",
		".env",
		".env.local",
		"sub/.env.production",
		".mcp.json",
		".claude/settings.json",
		".saker/hooks.json",
		".codex/config.toml",
		".sakerignore",
		"notes.tmp",
		"dir/backup~",
		".DS_Store",
		"deep/.DS_Store",
	}
	for _, p := range excluded {
		if !IsExcluded(p) {
			t.Errorf("IsExcluded(%q) = false, want true", p)
		}
		if ExcludedReason(p) == "" {
			t.Errorf("ExcludedReason(%q) empty for excluded path", p)
		}
	}

	allowed := []string{
		"report.md",
		"src/main.go",
		".github/workflows/ci.yml", // not on the conservative exclusion list
		"env.txt",
		".environment",
		"git-notes.md",
		"claude.md",
		"tmp/file.txt",
	}
	for _, p := range allowed {
		if IsExcluded(p) {
			t.Errorf("IsExcluded(%q) = true, want false", p)
		}
		if reason := ExcludedReason(p); reason != "" {
			t.Errorf("ExcludedReason(%q) = %q, want empty", p, reason)
		}
	}

	if !IsExcluded("") || !IsExcluded("\xff\xfe") {
		t.Error("invalid inputs must be treated as excluded")
	}
}
