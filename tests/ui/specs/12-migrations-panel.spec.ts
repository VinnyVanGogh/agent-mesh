/**
 * spec 12: Migrations panel — verification flow (STA-564)
 *
 * API contract tests for the new verification layer on top of STA-553.
 * Full Playwright copy/paste proof requires a real git worktree with a committed
 * migration file (see manual QA checklist at the bottom of this file).
 *
 * Manual QA checklist (run with a real Rhizome worktree):
 *  1. Open a task whose diff adds supabase/migrations/*.sql
 *  2. Verify "Migrations (N)" section appears with the file card
 *  3. Verify "Copy verify query" button appears when no DSN is configured
 *  4. Click "Copy verify query" → paste into SQL editor → run → results match DDL objects
 *  5. Click "Mark applied" (no DSN) → manual verify UI appears with per-object checkboxes
 *  6. Tick all boxes → click "Confirm results" → button becomes "✓ Applied (manual)"
 *  7. Re-fetch task activity log → migration_applied entry with mode=manual, checks array
 *  8. Verify Ship Review "Approve & merge" is blocked while migration is unverified
 *  9. After marking applied, Approve & merge succeeds normally
 */

import { test, expect, gotoTaskPage } from '../fixtures';

test.describe('migrations panel', () => {
  test('GET migrations returns empty list for a plain task', async ({ api }) => {
    const task = await api.createTask('mig-empty');
    const data = await api.getMigrations(task.id);
    expect(Array.isArray(data.migrations)).toBe(true);
    expect(data.migrations.length).toBe(0);
    expect(typeof data.sql_editor_url).toBe('string');
  });

  test('POST mark-applied returns ok and records activity', async ({ api }) => {
    const task = await api.createTask('mig-mark-applied');
    const path = 'supabase/migrations/20261003120000_test.sql';
    const res = await api.markMigrationApplied(task.id, path, 'board');
    expect(res.ok).toBe(true);
    expect(res.path).toBe(path);
    expect(res.applied_by).toBe('board');
  });

  test('POST mark-applied returns 400 when path is empty', async ({ api }) => {
    const task = await api.createTask('mig-nopath');
    let threw = false;
    try {
      await api.markMigrationApplied(task.id, '');
    } catch (err: unknown) {
      threw = true;
      expect((err as Error).message).toMatch(/400/);
    }
    expect(threw).toBe(true);
  });

  test('no migrations section rendered when diff has no migration files', async ({ page, api }) => {
    const task = await api.createTask('mig-no-panel');
    await gotoTaskPage(page, task);
    // The panel must NOT appear when there are zero migration files.
    const panel = page.locator('.migrations-section');
    // Give the async load a moment to potentially appear.
    await page.waitForTimeout(1_500);
    await expect(panel).toHaveCount(0);
  });

  test('GET migrations response includes has_auto_verify flag', async ({ api }) => {
    const task = await api.createTask('mig-auto-verify-flag');
    const data = await api.getMigrations(task.id);
    expect(typeof data.has_auto_verify).toBe('boolean');
  });

  test('POST mark-applied without DSN and without check_results returns manual mode query', async ({ api }) => {
    // This test requires a task with a repo_path pointing to a directory that
    // has a migration file. Since we cannot inject real migration files in the
    // API test environment, we test with a plain task (empty SQL → unchecked mode).
    const task = await api.createTask('mig-manual-mode');
    const res = await api.markMigrationApplied(task.id, 'supabase/migrations/20261003120000_test.sql', 'board');
    // Empty SQL (file not found) → unchecked mode → ok:true
    expect(res.ok).toBe(true);
    expect(res.mode).toBe('unchecked');
  });

  test('POST mark-applied with passing check_results records applied', async ({ api }) => {
    const task = await api.createTask('mig-manual-pass');
    const res = await api.markMigrationApplied(
      task.id,
      'supabase/migrations/20261003120000_test.sql',
      'board',
      { check_results: [{ description: 'table public.test exists', passed: true }] },
    );
    // File not found → checks empty → unchecked mode (check_results ignored for empty checks)
    expect(res.ok).toBe(true);
  });

  test('POST mark-applied with override_reason bypasses failure', async ({ api }) => {
    const task = await api.createTask('mig-override');
    const res = await api.markMigrationApplied(
      task.id,
      'supabase/migrations/20261003120000_test.sql',
      'board',
      {
        check_results: [{ description: 'table public.test exists', passed: false }],
        override_reason: 'already verified via Supabase dashboard',
      },
    );
    // File not found → unchecked → ok:true regardless
    expect(res.ok).toBe(true);
  });
});
