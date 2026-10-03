import { test, expect, gotoTaskPage } from '../fixtures';

// STA-529: run-control bar must show Resume when pause is engaged under the
// STA-525 step-boundary gate (task stays in_progress, never reaches 'paused'
// disposition, so the bar must sync from /run-control-state directly).

test('pause button becomes Resume immediately after clicking Pause', async ({ page, api }) => {
  const task = await api.createTask('RunCtrl-pause-live');
  await api.setStage(task.id, 'in_progress');
  await gotoTaskPage(page, task);

  const bar = page.locator(`#run-control-bar-${task.id}`);
  const pauseBtn = bar.locator('.run-ctrl-pause');

  await expect(pauseBtn).toBeVisible({ timeout: 5_000 });
  await expect(pauseBtn).toHaveText(/Pause after step/);

  // Click Pause — under the fix the bar must sync from run-control-state and
  // flip to Resume without any SSE disposition event or page reload.
  await pauseBtn.click();
  await expect(pauseBtn).toHaveText(/Resume/, { timeout: 3_000 });
});

test('bar shows Resume after page reload when paused via UI', async ({ page, api }) => {
  const task = await api.createTask('RunCtrl-pause-reload');
  await api.setStage(task.id, 'in_progress');
  await gotoTaskPage(page, task);

  const bar = page.locator(`#run-control-bar-${task.id}`);
  const pauseBtn = bar.locator('.run-ctrl-pause');

  await expect(pauseBtn).toBeVisible({ timeout: 5_000 });
  await pauseBtn.click();
  await expect(pauseBtn).toHaveText(/Resume/, { timeout: 3_000 });

  // Reload: buildRunControlBar must sync run-control-state and show Resume.
  await page.reload();
  await expect(page.locator('#task-page-content .task-page-title')).toHaveText(task.name, { timeout: 20_000 });
  const barAfter = page.locator(`#run-control-bar-${task.id}`);
  await expect(barAfter.locator('.run-ctrl-pause')).toHaveText(/Resume/, { timeout: 3_000 });
});

test('clicking Resume sends resume and restores Pause-after-step label', async ({ page, api }) => {
  const task = await api.createTask('RunCtrl-resume');
  await api.setStage(task.id, 'in_progress');
  await gotoTaskPage(page, task);

  const bar = page.locator(`#run-control-bar-${task.id}`);
  const pauseBtn = bar.locator('.run-ctrl-pause');

  await expect(pauseBtn).toBeVisible({ timeout: 5_000 });
  await pauseBtn.click();
  await expect(pauseBtn).toHaveText(/Resume/, { timeout: 3_000 });

  await pauseBtn.click();
  await expect(pauseBtn).toHaveText(/Pause after step/, { timeout: 3_000 });

  // Backend should now show paused=false.
  const state = await api.getRunControlState(task.id);
  expect(state.paused).toBe(false);
});
