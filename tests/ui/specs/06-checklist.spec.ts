import { test, expect } from '../fixtures';

// The throwaway daemon reports git commit "apitest", which is never in main,
// so the commit-hash DoD gate is always closed here. That makes the gate's
// blocked state deterministic to assert.

test('checklist shows items and the DoD commit gate status', async ({ page }) => {
  await page.goto('/checklist');
  const view = page.locator('#view-checklist');
  await expect(view).toHaveClass(/active/);

  await expect(page.locator('#checklist-progress')).toHaveText(/\d+\/\d+ reviewed/);
  const banner = page.locator('.checklist-commit-gate-banner');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('Commit-Hash Gate');
  await expect(banner).toContainText('Running Daemon: apitest');

  await banner.locator('.cl-gate-toggle-btn').click();
  await expect(banner.locator('.cl-gate-log-table tbody tr').first()).toBeVisible();

  await expect(page.locator('#checklist-container').getByText('Commit Gate Active').first()).toBeVisible();
});
