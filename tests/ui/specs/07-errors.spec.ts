import { test, expect, knownBug } from '../fixtures';

test('an unknown task id shows an error, not a blank page', async ({ page }) => {
  await page.goto('/tasks/STA/ui-e2e/task-doesnotexist');
  const content = page.locator('#task-page-content');
  await expect(content).toContainText(/not found|failed to load/i);
  await expect(content.locator('.task-page-title')).toHaveCount(0);
});

test('an unknown route is a 404, not the app shell', async ({ page }) => {
  const res = await page.goto('/definitely-not-a-view');
  expect(res?.status()).toBe(404);
});

// Uses a second throwaway daemon that scripts/ui-e2e.sh starts for this test
// alone, so killing it does not affect the other specs.
test('the UI shows an error when the daemon goes down', async ({ page, request }) => {
  const downURL = process.env.STAYPOINT_UI_DOWN_BASE_URL;
  const downPID = Number(process.env.STAYPOINT_UI_DOWN_PID);
  test.skip(!downURL || !downPID, 'scripts/ui-e2e.sh did not start the expendable daemon');
  const token = process.env.STAYPOINT_API_TOKEN!;

  const res = await request.post(`${downURL}/api/tasks`, {
    headers: { Authorization: `Bearer ${token}` },
    data: { name: 'Daemon down', organization: 'STA', project: 'ui-e2e' },
  });
  expect(res.ok()).toBeTruthy();
  const task = await res.json();

  await page.goto(`${downURL}/?token=${encodeURIComponent(token)}`);
  await expect(page.locator('#conn-badge')).toHaveText('live');

  process.kill(downPID, 'SIGTERM');

  await expect(page.locator('#conn-badge')).toHaveText(/reconnecting|offline|error/i, { timeout: 15_000 });
  await expect(page.locator('#conn-badge')).toHaveClass(/badge-error/);

  // Opening a task needs the API, which is gone. Showing the copy cached at
  // page load, with no sign it may be stale, is the bug this half covers.
  knownBug('STA-379');
  // Navigating inside the already-loaded app needs the API, which is gone.
  await page.evaluate(path => {
    history.pushState({}, '', path);
    window.dispatchEvent(new PopStateEvent('popstate', { state: { taskPage: true } }));
  }, `/tasks/STA/ui-e2e/${task.id}`);
  await expect(page.locator('#task-page-content')).toContainText(/failed to load|unreachable|offline|error/i);
});
