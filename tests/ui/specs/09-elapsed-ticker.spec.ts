import { test, expect, gotoTaskPage, simulatePartialRun } from '../fixtures';

// Regression test for STA-502: stopChatPoll() was calling stopElapsedTicker(),
// so the ticker died immediately after renderTaskPage started it.

test('Elapsed ticks every second between steps on a running task', async ({ page, api }) => {
  const task = await api.createTask('Elapsed ticker');

  // Seed a wake step (no terminal state) so runElapsedMs has a start time.
  const persisted = simulatePartialRun(task.id);
  expect(persisted, 'stepsim persisted no steps').toBeGreaterThan(0);

  await gotoTaskPage(page, task);

  const statsBar = page.locator(`#timeline-stats-${task.id}`);
  await expect(statsBar).toBeVisible({ timeout: 5_000 });

  const elapsedStat = statsBar.locator('.timeline-stat', { has: page.locator('.timeline-stat-label', { hasText: 'Elapsed' }) });
  const elapsedVal = elapsedStat.locator('.timeline-stat-val');

  // Capture two readings 3 seconds apart; they must differ, proving the ticker runs.
  const before = await elapsedVal.textContent();
  await page.waitForTimeout(3_000);
  const after = await elapsedVal.textContent();

  expect(after, 'Elapsed did not change — ticker not running (STA-502 regression)').not.toBe(before);
});
