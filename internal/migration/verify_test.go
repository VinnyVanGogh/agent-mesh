package migration_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/VinnyVanGogh/staypoint/internal/migration"
)

// ------------- Parser tests -----------------------------------------------

// corporateValuesMigration mirrors the sample migration referenced in STA-564.
const corporateValuesMigration = `-- 20261003120000_corporate_values.sql
CREATE TABLE IF NOT EXISTS corporate_values (
    id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    key  text NOT NULL UNIQUE,
    label text NOT NULL,
    sort_order int NOT NULL DEFAULT 0
);

ALTER TABLE corporate_values ENABLE ROW LEVEL SECURITY;

CREATE POLICY cv_select ON corporate_values FOR SELECT USING (true);

CREATE INDEX idx_cv_sort ON corporate_values (sort_order);

INSERT INTO corporate_values (key, label, sort_order) VALUES
    ('integrity', 'Integrity', 1),
    ('collaboration', 'Collaboration', 2),
    ('excellence', 'Excellence', 3);
`

func TestParseChecks_CorporateValues(t *testing.T) {
	checks := migration.ParseChecks(corporateValuesMigration)
	if len(checks) == 0 {
		t.Fatal("expected checks, got none")
	}

	kindsSeen := map[migration.CheckKind]bool{}
	for _, c := range checks {
		kindsSeen[c.Kind] = true
		if c.SQL == "" && c.Kind != migration.KindUnchecked {
			t.Errorf("check %q has empty SQL", c.Description)
		}
	}

	required := []migration.CheckKind{
		migration.KindRegclass,
		migration.KindRLS,
		migration.KindPolicy,
		migration.KindIndex,
		migration.KindRows,
	}
	for _, k := range required {
		if !kindsSeen[k] {
			t.Errorf("expected check kind %q, not found in %v", k, checks)
		}
	}
}

func TestParseChecks_CreateTableQualified(t *testing.T) {
	sql := `CREATE TABLE public.sites (id uuid PRIMARY KEY);`
	checks := migration.ParseChecks(sql)
	found := false
	for _, c := range checks {
		if c.Kind == migration.KindRegclass && strings.Contains(c.SQL, "public.sites") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected regclass check for public.sites, got %v", checks)
	}
}

func TestParseChecks_AddColumn(t *testing.T) {
	sql := `ALTER TABLE profiles ADD COLUMN avatar_url text;`
	checks := migration.ParseChecks(sql)
	found := false
	for _, c := range checks {
		if c.Kind == migration.KindColumn && strings.Contains(c.SQL, "avatar_url") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected column check for avatar_url, got %v", checks)
	}
}

func TestParseChecks_CreateFunction(t *testing.T) {
	sql := `CREATE OR REPLACE FUNCTION update_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.updated_at = now(); RETURN NEW; END $$;`
	checks := migration.ParseChecks(sql)
	found := false
	for _, c := range checks {
		if c.Kind == migration.KindFunction && strings.Contains(c.SQL, "update_updated_at") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected function check for update_updated_at, got %v", checks)
	}
}

func TestParseChecks_NoFalsePositiveInComments(t *testing.T) {
	sql := `-- CREATE TABLE ignored (id uuid);
/* CREATE POLICY also_ignored ON foo FOR SELECT USING (true); */
CREATE TABLE real_table (id uuid PRIMARY KEY);`
	checks := migration.ParseChecks(sql)
	for _, c := range checks {
		if strings.Contains(c.Description, "ignored") || strings.Contains(c.Description, "also_ignored") {
			t.Errorf("commented-out DDL produced a check: %v", c)
		}
	}
	found := false
	for _, c := range checks {
		if strings.Contains(c.Description, "real_table") {
			found = true
		}
	}
	if !found {
		t.Errorf("real_table check not produced")
	}
}

func TestBuildVerificationQuery_Empty(t *testing.T) {
	q := migration.BuildVerificationQuery(nil)
	if !strings.Contains(q, "WHERE false") {
		t.Errorf("expected no-op query for empty checks, got: %s", q)
	}
}

func TestBuildVerificationQuery_Roundtrip(t *testing.T) {
	checks := migration.ParseChecks(corporateValuesMigration)
	q := migration.BuildVerificationQuery(checks)
	if q == "" {
		t.Fatal("expected non-empty verification query")
	}
	// Should have UNION ALL
	if len(checks) > 1 && !strings.Contains(q, "UNION ALL") {
		t.Errorf("expected UNION ALL for multiple checks, got: %s", q)
	}
}

// ------------- Postgres integration tests ---------------------------------
// These run only when TEST_PG_DSN is set or a local PG is available.

func testPGDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("TEST_PG_DSN"); dsn != "" {
		return dsn
	}
	// Try local default
	dsn := "postgres://localhost/postgres?sslmode=disable"
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	return dsn
}

func TestRunChecks_AutoMode_BeforeAndAfter(t *testing.T) {
	dsn := testPGDSN(t)
	ctx := context.Background()

	// Create throwaway schema
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	schema := fmt.Sprintf("sta564_test_%d", os.Getpid())
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") //nolint:errcheck

	migSQL := fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s.corporate_values (
    id   serial PRIMARY KEY,
    key  text NOT NULL UNIQUE,
    label text NOT NULL,
    sort_order int NOT NULL DEFAULT 0
);
ALTER TABLE %s.corporate_values ENABLE ROW LEVEL SECURITY;
CREATE POLICY cv_select ON %s.corporate_values FOR SELECT USING (true);
CREATE INDEX idx_%s_cv_sort ON %s.corporate_values (sort_order);
INSERT INTO %s.corporate_values (key, label, sort_order) VALUES
    ('integrity', 'Integrity', 1),
    ('collaboration', 'Collaboration', 2);
`, schema, schema, schema, schema, schema, schema)

	checks := migration.ParseChecks(migSQL)
	if len(checks) == 0 {
		t.Fatal("no checks generated")
	}

	// --- Before applying: all checks should fail ---
	resultsBefore, err := migration.RunChecks(ctx, dsn, checks)
	if err != nil {
		t.Fatalf("RunChecks before: %v", err)
	}
	if migration.AllPassed(resultsBefore) {
		t.Error("expected at least one failure before migration is applied")
	}

	// --- Apply the migration ---
	if _, err := db.ExecContext(ctx, migSQL); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	// --- After applying: all checks should pass ---
	resultsAfter, err := migration.RunChecks(ctx, dsn, checks)
	if err != nil {
		t.Fatalf("RunChecks after: %v", err)
	}
	if !migration.AllPassed(resultsAfter) {
		failed := migration.FailedDescriptions(resultsAfter)
		t.Errorf("expected all checks to pass after migration; failed: %v", failed)
	}
}

func TestRunChecks_ReadOnly(t *testing.T) {
	dsn := testPGDSN(t)
	ctx := context.Background()

	// Attempt a write inside the verify transaction via a crafted "check".
	writeCheck := migration.Check{
		Kind:        migration.KindRows,
		Description: "write attempt (should fail)",
		SQL:         "SELECT (INSERT INTO pg_catalog.pg_class DEFAULT VALUES RETURNING 1) AS ok",
	}
	results, err := migration.RunChecks(ctx, dsn, []migration.Check{writeCheck})
	// Either err != nil or the check failed — a write must not succeed
	if err == nil && len(results) > 0 && results[0].Passed {
		t.Error("write inside read-only transaction should not pass")
	}
}
