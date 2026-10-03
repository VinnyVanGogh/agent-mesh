// Package migration provides helpers for detecting, reading, and risk-checking
// DB migration files within a task's changed file set.
package migration

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultGlobs are the migration file patterns used when no project config overrides them.
var DefaultGlobs = []string{
	"supabase/migrations/*.sql",
	"prisma/migrations/**/migration.sql",
	"migrations/*.sql",
	"db/migrate/*",
}

// File holds a detected migration file with its content and risk analysis.
type File struct {
	Path          string   `json:"path"`
	SQL           string   `json:"sql"`
	RiskStatements []string `json:"risk_statements"`
	AdditiveOnly  bool     `json:"additive_only"`
}

// destructivePatterns is compiled once at startup.
var destructivePatterns = []*regexp.Regexp{
	// DROP TABLE / VIEW / INDEX / SEQUENCE / FUNCTION / etc.
	regexp.MustCompile(`(?i)\bDROP\s+\w`),
	// TRUNCATE
	regexp.MustCompile(`(?i)\bTRUNCATE\b`),
	// DELETE without WHERE
	regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+\S+\s*(?:;|$)`),
	// UPDATE without WHERE (bare statement ending in ; with no WHERE clause on the same line)
	regexp.MustCompile(`(?i)\bUPDATE\s+\S+\s+SET\b[^;]*;`),
	// ALTER TABLE … DROP COLUMN
	regexp.MustCompile(`(?i)\bALTER\s+TABLE\b.*\bDROP\s+COLUMN\b`),
	// ALTER TABLE … ALTER COLUMN … TYPE
	regexp.MustCompile(`(?i)\bALTER\s+TABLE\b.*\bALTER\s+COLUMN\b.*\bTYPE\b`),
	// ALTER TABLE … SET NOT NULL (risky on existing populated tables)
	regexp.MustCompile(`(?i)\bALTER\s+TABLE\b.*\bSET\s+NOT\s+NULL\b`),
	// DISABLE ROW LEVEL SECURITY
	regexp.MustCompile(`(?i)\bDISABLE\s+ROW\s+LEVEL\s+SECURITY\b`),
	// DROP POLICY
	regexp.MustCompile(`(?i)\bDROP\s+POLICY\b`),
}

// stripComments removes -- line comments and /* */ block comments from sql.
func stripComments(sql string) string {
	var b strings.Builder
	i := 0
	for i < len(sql) {
		// Line comment
		if i+1 < len(sql) && sql[i] == '-' && sql[i+1] == '-' {
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		}
		// Block comment
		if i+1 < len(sql) && sql[i] == '/' && sql[i+1] == '*' {
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			i += 2 // skip */
			continue
		}
		b.WriteByte(sql[i])
		i++
	}
	return b.String()
}

// CheckRisk scans sql for destructive or locking patterns.
// Returns the matched snippets and whether any were found.
func CheckRisk(sql string) (risks []string, hasRisk bool) {
	stripped := stripComments(sql)
	lines := strings.Split(stripped, "\n")
	seen := map[string]bool{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, pat := range destructivePatterns {
			if pat.FindStringIndex(trimmed) != nil {
				snippet := trimmed
				if len(snippet) > 80 {
					snippet = snippet[:80] + "…"
				}
				if !seen[snippet] {
					seen[snippet] = true
					risks = append(risks, snippet)
				}
			}
		}
	}
	return risks, len(risks) > 0
}

// matchGlob reports whether path matches any of the globs.
// Each glob is matched against the path using filepath.Match; the full path
// and the path with each leading directory component stripped are tried so that
// globs like "supabase/migrations/*.sql" match relative paths from the repo root.
func matchGlob(path string, globs []string) bool {
	for _, g := range globs {
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
		// Try matching just the file name for simple glob patterns like "*.sql"
		if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
			return true
		}
		// Try prefix-stripped paths: for "supabase/migrations/*.sql" vs "supabase/migrations/foo.sql"
		// filepath.Match already handles this when g has no **, but Glob-style ** is not supported natively.
		// We handle ** by matching against progressively shorter path suffixes.
		if strings.Contains(g, "**") {
			if matchDoubleGlob(path, g) {
				return true
			}
		}
	}
	return false
}

// matchDoubleGlob handles simple ** patterns by splitting on ** and testing prefix/suffix.
func matchDoubleGlob(path, glob string) bool {
	parts := strings.SplitN(glob, "**", 2)
	if len(parts) != 2 {
		return false
	}
	prefix, suffix := parts[0], parts[1]
	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false
	}
	remainder := path
	if prefix != "" {
		remainder = path[len(prefix):]
	}
	if suffix == "" {
		return true
	}
	// suffix starts with "/" – match it at any directory depth
	suf := strings.TrimPrefix(suffix, "/")
	ok, _ := filepath.Match(suf, filepath.Base(remainder))
	if ok {
		return true
	}
	// walk subdirs
	return strings.HasSuffix(remainder, strings.TrimPrefix(suffix, "/"))
}

// Detect filters filePaths to those matching any of globs.
func Detect(filePaths, globs []string) []string {
	var matched []string
	for _, p := range filePaths {
		if matchGlob(p, globs) {
			matched = append(matched, p)
		}
	}
	SortByTimestamp(matched)
	return matched
}

// SortByTimestamp sorts migration paths lexicographically (timestamp prefixes sort naturally).
func SortByTimestamp(paths []string) {
	sort.Strings(paths)
}

// ReadContent reads the current content of a migration file from workDir.
func ReadContent(workDir, relPath string) (string, error) {
	full := filepath.Join(workDir, relPath)
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
