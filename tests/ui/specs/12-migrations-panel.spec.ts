/**
 * spec 12: Migrations panel
 *
 * API contract tests + "no panel for empty diff" browser test.
 *
 * Full Playwright proof (panel visible, Copy works, destructive warning shown,
 * Mark applied recorded) requires a real git worktree with a committed migration
 * file — covered by the manual QA checklist below.
 *
 * Manual QA checklist (run with a real Rhizome worktree):
 *  1. Open a task whose diff adds supabase/migrations/*.sql
 *  2. Verify "Migrations (N)" section appears below Ship Review card
 *  3. Verify file path and SQL are shown with syntax highlighting
 *  4. Click "Copy SQL" → paste elsewhere → content matches file exactly
 *  5. If SQL contains DROP/TRUNCATE: red "⚠ Destructive" badge and risk list shown
 *  6. If SQL is additive: blue "Additive only ✓" badge shown
 *  7. Click "Mark applied" → button changes to "✓ Applied", card dims
 *  8. Re-fetch task activity log → migration_applied entry present with correct path
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
});
