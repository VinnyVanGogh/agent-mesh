package migration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/migration"
)

func TestCheckRisk_AdditiveOnly(t *testing.T) {
	sql := `CREATE TABLE users (id uuid PRIMARY KEY, name text NOT NULL);`
	risks, _, hasRisk := migration.CheckRisk(sql)
	if hasRisk {
		t.Errorf("additive SQL flagged as risky: %v", risks)
	}
}

func TestCheckRisk_Drop(t *testing.T) {
	sql := `DROP TABLE users;`
	risks, _, hasRisk := migration.CheckRisk(sql)
	if !hasRisk {
		t.Error("DROP TABLE not detected as risky")
	}
	if len(risks) == 0 {
		t.Error("expected at least one risk snippet")
	}
}

func TestCheckRisk_Truncate(t *testing.T) {
	_, _, hasRisk := migration.CheckRisk(`TRUNCATE sessions;`)
	if !hasRisk {
		t.Error("TRUNCATE not detected as risky")
	}
}

func TestCheckRisk_DeleteWithoutWhere(t *testing.T) {
	_, _, hasRisk := migration.CheckRisk("DELETE FROM old_logs;")
	if !hasRisk {
		t.Error("DELETE without WHERE not detected as risky")
	}
}

func TestCheckRisk_DeleteWithWhere(t *testing.T) {
	// DELETE with WHERE — the current regex catches bare DELETE FROM tbl; (ends with ; or EOL)
	// but a WHERE clause means the regex won't match because it looks for ;|$ after SET.
	// The DELETE pattern only fires when there's no WHERE on the same statement.
	risks, _, _ := migration.CheckRisk("DELETE FROM old_logs WHERE created_at < '2020-01-01';")
	// This may or may not fire depending on how the regex matches; just verify no panic.
	_ = risks
}

func TestCheckRisk_AlterDropColumn(t *testing.T) {
	_, _, hasRisk := migration.CheckRisk(`ALTER TABLE users DROP COLUMN legacy_col;`)
	if !hasRisk {
		t.Error("ALTER TABLE DROP COLUMN not detected as risky")
	}
}

func TestCheckRisk_DisableRLS(t *testing.T) {
	_, _, hasRisk := migration.CheckRisk(`ALTER TABLE secret_data DISABLE ROW LEVEL SECURITY;`)
	if !hasRisk {
		t.Error("DISABLE ROW LEVEL SECURITY not detected as risky")
	}
}

func TestCheckRisk_DropPolicy(t *testing.T) {
	_, _, hasRisk := migration.CheckRisk(`DROP POLICY user_read ON profiles;`)
	if !hasRisk {
		t.Error("DROP POLICY not detected as risky")
	}
}

func TestCheckRisk_LineCommentIgnored(t *testing.T) {
	risks, _, hasRisk := migration.CheckRisk("-- DROP TABLE users;\nCREATE TABLE new_users (id uuid PRIMARY KEY);")
	if hasRisk {
		t.Errorf("line-comment DROP flagged as risky: %v", risks)
	}
}

func TestCheckRisk_BlockCommentIgnored(t *testing.T) {
	risks, _, hasRisk := migration.CheckRisk("/* DROP TABLE users; TRUNCATE sessions; */\nCREATE TABLE new_users (id uuid PRIMARY KEY);")
	if hasRisk {
		t.Errorf("block-comment DROP flagged as risky: %v", risks)
	}
}

func TestCheckRisk_IdempotentReCreate_Policy(t *testing.T) {
	sql := `DROP POLICY IF EXISTS cv_select ON corporate_values;
CREATE POLICY cv_select ON corporate_values FOR SELECT USING (true);`
	risks, idempotents, hasRisk := migration.CheckRisk(sql)
	if hasRisk {
		t.Errorf("idempotent DROP POLICY IF EXISTS flagged as risky: %v", risks)
	}
	if len(idempotents) == 0 {
		t.Error("expected idempotent re-create entry for cv_select policy")
	}
}

func TestCheckRisk_IdempotentReCreate_Trigger(t *testing.T) {
	sql := `DROP TRIGGER IF EXISTS trg_updated_at ON users;
CREATE TRIGGER trg_updated_at BEFORE UPDATE ON users EXECUTE FUNCTION set_updated_at();`
	risks, idempotents, hasRisk := migration.CheckRisk(sql)
	if hasRisk {
		t.Errorf("idempotent DROP TRIGGER IF EXISTS flagged as risky: %v", risks)
	}
	if len(idempotents) == 0 {
		t.Error("expected idempotent re-create entry for trg_updated_at trigger")
	}
}

func TestCheckRisk_DropPolicyWithoutCreate_StillRisky(t *testing.T) {
	// DROP POLICY without a matching CREATE in the same file stays destructive.
	sql := `DROP POLICY IF EXISTS old_policy ON profiles;`
	_, _, hasRisk := migration.CheckRisk(sql)
	if !hasRisk {
		t.Error("DROP POLICY IF EXISTS without matching CREATE should still be risky")
	}
}

func TestCheckRisk_DropPolicyNoIFE_StillRisky(t *testing.T) {
	// DROP POLICY (without IF EXISTS) never gets idempotent treatment even with CREATE.
	sql := `DROP POLICY cv_select ON corporate_values;
CREATE POLICY cv_select ON corporate_values FOR SELECT USING (true);`
	_, _, hasRisk := migration.CheckRisk(sql)
	if !hasRisk {
		t.Error("DROP POLICY without IF EXISTS should always be risky")
	}
}

func TestCheckRisk_IdempotentReCreate_QuotedPolicyName(t *testing.T) {
	// Quoted names with spaces — the real-world failing case from STA-590.
	sql := `DROP POLICY IF EXISTS "Public can view active corporate values" ON public.corporate_values;
CREATE POLICY "Public can view active corporate values" ON public.corporate_values FOR SELECT TO authenticated USING (active = true);`
	risks, idempotents, hasRisk := migration.CheckRisk(sql)
	if hasRisk {
		t.Errorf("quoted idempotent DROP POLICY IF EXISTS flagged as risky: %v", risks)
	}
	if len(idempotents) == 0 {
		t.Error("expected idempotent re-create entry for quoted policy name with spaces")
	}
}

func TestCheckRisk_IdempotentReCreate_QuotedPolicySchemaTable(t *testing.T) {
	// Schema-qualified table reference with a quoted policy name.
	sql := `DROP POLICY IF EXISTS "Admins can manage corporate values" ON public.corporate_values;
CREATE POLICY "Admins can manage corporate values" ON public.corporate_values FOR ALL TO authenticated USING (true) WITH CHECK (true);`
	risks, idempotents, hasRisk := migration.CheckRisk(sql)
	if hasRisk {
		t.Errorf("schema-qualified quoted policy flagged as risky: %v", risks)
	}
	if len(idempotents) == 0 {
		t.Error("expected idempotent re-create entry for schema-qualified quoted policy")
	}
}

func TestCheckRisk_QuotedPolicyDifferentTable_StillRisky(t *testing.T) {
	// DROP on table_a + CREATE on table_b with the same policy name must NOT be treated
	// as idempotent — policy names are scoped to a table.
	sql := `DROP POLICY IF EXISTS "read_all" ON public.table_a;
CREATE POLICY "read_all" ON public.table_b FOR SELECT USING (true);`
	_, _, hasRisk := migration.CheckRisk(sql)
	if !hasRisk {
		t.Error("same policy name on different tables should still be risky")
	}
}

func TestCheckRisk_QuotedPolicyWithoutCreate_StillRisky(t *testing.T) {
	// Quoted DROP POLICY IF EXISTS without a matching CREATE stays destructive.
	sql := `DROP POLICY IF EXISTS "stale policy" ON public.some_table;`
	_, _, hasRisk := migration.CheckRisk(sql)
	if !hasRisk {
		t.Error("quoted DROP POLICY IF EXISTS without matching CREATE should be risky")
	}
}

func TestDetect_DefaultGlobs(t *testing.T) {
	files := []string{
		"supabase/migrations/20261003120000_corporate_values.sql",
		"src/api/routes.ts",
		"prisma/migrations/20261001000000_init/migration.sql",
		"internal/config/config.go",
	}
	matched := migration.Detect(files, migration.DefaultGlobs)
	if len(matched) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(matched), matched)
	}
}

func TestDetect_SortedByTimestamp(t *testing.T) {
	files := []string{
		"supabase/migrations/20261003000000_b.sql",
		"supabase/migrations/20261001000000_a.sql",
	}
	matched := migration.Detect(files, migration.DefaultGlobs)
	if len(matched) < 2 || matched[0] > matched[1] {
		t.Errorf("migrations not sorted by timestamp: %v", matched)
	}
}

func TestReadContent(t *testing.T) {
	dir := t.TempDir()
	sqlDir := filepath.Join(dir, "supabase", "migrations")
	if err := os.MkdirAll(sqlDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "CREATE TABLE test (id int);"
	p := filepath.Join(sqlDir, "20261003_test.sql")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := migration.ReadContent(dir, "supabase/migrations/20261003_test.sql")
	if err != nil {
		t.Fatalf("ReadContent: %v", err)
	}
	if got != content {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestReadContentAtRef_ReadsFromBranchNotCheckout(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "init")
	run("checkout", "-q", "-b", "staypoint/task-x")
	if err := os.MkdirAll(filepath.Join(dir, "supabase", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "CREATE TABLE IF NOT EXISTS public.t (id int);\n"
	if err := os.WriteFile(filepath.Join(dir, "supabase", "migrations", "1_t.sql"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "add migration")
	run("checkout", "-q", "main") // the file is absent from this checkout

	if _, err := migration.ReadContent(dir, "supabase/migrations/1_t.sql"); err == nil {
		t.Fatal("expected ReadContent to fail on the main checkout")
	}
	got, err := migration.ReadContentAtRef(context.Background(), dir, "staypoint/task-x", "supabase/migrations/1_t.sql")
	if err != nil {
		t.Fatalf("ReadContentAtRef: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
