import { test, expect, gotoTaskPage } from '../fixtures';
import type { APIRequestContext } from '@playwright/test';

// STA-535 regression coverage for ship-review UI bugs:
//   Bug 1 — dev_url missing from GET after UpsertCard auto-start
//   Bug 2 — head_moved path was dead code (apiFetch threw before JSON parse)
//   Bug 3 — approved / rejected final state was never rendered

const TOKEN = process.env.STAYPOINT_API_TOKEN || '';

async function upsertShipReview(
  request: APIRequestContext,
  taskId: string,
  opts: { testSteps?: string[]; devUrl?: string } = {},
) {
  const res = await request.put(`/api/tasks/${encodeURIComponent(taskId)}/ship-review`, {
    headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' },
    data: {
      test_steps: opts.testSteps ?? ['1. Open the homepage', '2. Verify title'],
      ...(opts.devUrl ? { dev_url: opts.devUrl } : {}),
    },
  });
  return res;
}

test.describe('ship review card', () => {

  // ── Bug 1: Preview row visible when dev_url is present ────────────────────
  test('Preview row shows dev_url from card', async ({ page, api, request }) => {
    const task = await api.createTask('Ship review dev_url');
    const res = await upsertShipReview(request, task.id, { devUrl: 'http://127.0.0.1:8799' });
    expect(res.ok(), `upsert failed: ${await res.text()}`).toBeTruthy();

    await gotoTaskPage(page, task);

    // The card section must appear.
    const card = page.locator('.ship-review-card');
    await expect(card).toBeVisible({ timeout: 10_000 });

    // Preview row with the URL must be visible.
    const previewRow = card.locator('.ship-review-row', { hasText: 'Preview' });
    await expect(previewRow).toBeVisible();
    const link = previewRow.locator('.ship-review-dev-link');
    await expect(link).toContainText('127.0.0.1:8799');
  });

  // ── Bug 2: head_moved alert includes new SHA ──────────────────────────────
  test('Approve shows head_moved alert with new SHA when 409 head_moved', async ({ page, api, request }) => {
    const task = await api.createTask('Ship review head_moved');
    const res = await upsertShipReview(request, task.id);
    expect(res.ok(), `upsert failed: ${await res.text()}`).toBeTruthy();

    await gotoTaskPage(page, task);
    const card = page.locator('.ship-review-card');
    await expect(card).toBeVisible({ timeout: 10_000 });

    // Intercept the approve call and return a 409 head_moved response.
    const fakeNewHead = 'abcdef123456deadbeef';
    await page.route('**/ship-review/approve', async (route) => {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'head_moved', new_head_sha: fakeNewHead }),
      });
    });

    // Auto-accept the confirm dialog (the "Approve and merge?" native confirm).
    page.once('dialog', (d) => d.accept());

    // Capture the alert that follows the 409.
    let alertMessage = '';
    page.on('dialog', async (d) => {
      alertMessage = d.message();
      await d.accept();
    });

    await card.getByRole('button', { name: /Approve/i }).click();

    // Wait for the alert to fire and be captured.
    await expect.poll(() => alertMessage, { timeout: 5_000 })
      .toContain(fakeNewHead.slice(0, 8));

    // The alert must say "HEAD moved" and not just "409 Conflict" (dead-code path).
    expect(alertMessage).toMatch(/moved/i);

    // The approve button must still be present (card reload, not removed).
    await expect(card).toBeVisible({ timeout: 5_000 });
  });

  // ── Bug 3: approved final state shows reviewed SHA → main SHA ─────────────
  test('Approve renders approved final state card with main SHA', async ({ page, api, request }) => {
    const task = await api.createTask('Ship review approve final');
    const upsert = await upsertShipReview(request, task.id);
    expect(upsert.ok(), `upsert failed: ${await upsert.text()}`).toBeTruthy();

    // Get the current head_sha from the card so we can verify it in the final state.
    const cardRes = await request.get(`/api/tasks/${encodeURIComponent(task.id)}/ship-review`, {
      headers: { Authorization: `Bearer ${TOKEN}` },
    });
    const cardData = await cardRes.json();
    const headSHA = cardData.head_sha as string;

    await gotoTaskPage(page, task);
    const section = page.locator('.ship-review-card');
    await expect(section).toBeVisible({ timeout: 10_000 });

    const fakeMainSHA = 'f00dbadf00dbadf00d';
    // Intercept approve to return success with a known main_sha.
    await page.route('**/ship-review/approve', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          main_sha: fakeMainSHA,
          card: { status: 'approved', head_sha: headSHA, main_sha: fakeMainSHA },
        }),
      });
    });

    // Auto-accept the confirm dialog.
    page.once('dialog', (d) => d.accept());

    await page.locator('.ship-review-card .ship-review-approve-btn').click();

    // The action buttons must disappear.
    await expect(page.locator('.ship-review-approve-btn')).toHaveCount(0, { timeout: 5_000 });

    // The final-state card must be visible.
    const finalCard = page.locator('.ship-review-card--final');
    await expect(finalCard).toBeVisible({ timeout: 5_000 });

    // It must show the approved badge.
    await expect(finalCard.locator('.ship-review-status-badge')).toContainText(/Approved/i);

    // It must show main SHA.
    await expect(finalCard).toContainText(fakeMainSHA.slice(0, 12));
  });

  // ── Bug 3b: rejected final state shows reject comment ────────────────────
  test('Reject renders rejected final state card with comment', async ({ page, api, request }) => {
    const task = await api.createTask('Ship review reject final');
    const upsert = await upsertShipReview(request, task.id);
    expect(upsert.ok(), `upsert failed: ${await upsert.text()}`).toBeTruthy();

    await gotoTaskPage(page, task);
    const section = page.locator('.ship-review-card');
    await expect(section).toBeVisible({ timeout: 10_000 });

    // Intercept reject to return success.
    await page.route('**/ship-review/reject', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: 'ok' }),
      });
    });

    const rejectComment = 'Tests failed on mobile';

    // Handle dialogs: first prompt (reason), then confirm (delete branch).
    const dialogs: import('@playwright/test').Dialog[] = []; // eslint-disable-line @typescript-eslint/no-unused-vars
    page.on('dialog', async (d) => {
      dialogs.push(d);
      if (d.type() === 'prompt') await d.accept(rejectComment);
      else if (d.type() === 'confirm') await d.dismiss(); // don't delete branch
      else await d.accept();
    });

    await page.locator('.ship-review-card .ship-review-reject-btn').click();

    // The action buttons must disappear.
    await expect(page.locator('.ship-review-reject-btn')).toHaveCount(0, { timeout: 5_000 });

    // The final-state card must be visible with rejected badge.
    const finalCard = page.locator('.ship-review-card--final');
    await expect(finalCard).toBeVisible({ timeout: 5_000 });
    await expect(finalCard.locator('.ship-review-status-badge')).toContainText(/Rejected/i);

    // The reject comment must appear in the final card.
    await expect(finalCard).toContainText(rejectComment);
  });

});
