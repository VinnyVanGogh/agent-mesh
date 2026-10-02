import { test, expect, knownBug, gotoTaskPage, simulateRunSteps } from '../fixtures';

// Run Now is the web UI's only control that changes a task's status
// (it POSTs stage=in_progress), so it also covers "status transition via UI".

test('Run Now moves a fresh task to in_progress', async ({ page, api }) => {
  knownBug('RUN-NOW-ACTIVE');
  const task = await api.createTask('Run now');
  expect(task.execution_stage).toBe('todo');

  await gotoTaskPage(page, task);
  const content = page.locator('#task-page-content');

  const runBtn = content.locator('.run-now-btn');
  await expect(runBtn).toBeVisible({ timeout: 3_000 });
  await runBtn.click();
  await expect(runBtn).toHaveText(/Started/);

  await expect.poll(async () => (await api.getTask(task.id)).execution_stage).toBe('in_progress');
  // The page re-renders itself after a successful start.
  const stage = content.locator('.panel-field', { has: page.locator('.panel-field-label', { hasText: 'Stage' }) });
  await expect(stage.locator('.panel-field-value')).toHaveText('in_progress');
});

test('run timeline shows the steps a run recorded', async ({ page, api }) => {
  knownBug('RUN-STEPS-SCHEMA');
  const task = await api.createTask('Timeline');

  await gotoTaskPage(page, task);
  const steps = page.locator(`#timeline-steps-${task.id}`);
  await expect(steps.locator('.timeline-empty')).toBeVisible();

  const persisted = simulateRunSteps(task.id);
  expect(persisted, 'StepRecorder persisted no steps (see daemon/stepsim log)').toBeGreaterThan(0);

  await page.reload();
  await expect(page.locator('#task-page-content .task-page-title')).toHaveText(task.name, { timeout: 20_000 });
  await expect(steps.locator('.timeline-row')).toHaveCount(persisted);
  await expect(steps.locator('.timeline-row-wake .timeline-title')).toContainText('Run Now');
  await expect(page.locator(`#timeline-stats-${task.id} .timeline-stat-val`).first()).toHaveText(String(persisted));
});
