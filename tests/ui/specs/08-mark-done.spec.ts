import { test, expect, gotoTaskPage } from '../fixtures';

// Mark done button: visible for in_review tasks, calls POST /api/tasks/{id}/done,
// surfaces error inline (no work product → 409, watchdog block → 500).

test('Mark done button is visible when execution_stage is in_review', async ({ page, api, request }) => {
  const task = await api.createTask('Mark done visible');

  // Move the task to in_review via the stage endpoint.
  const headers = { Authorization: `Bearer ${process.env.STAYPOINT_API_TOKEN || ''}`, 'Content-Type': 'application/json' };
  const stageRes = await request.post(`/api/tasks/${encodeURIComponent(task.id)}/stage`, {
    headers,
    data: { stage: 'in_review' },
  });
  expect(stageRes.ok(), `stage→in_review failed: ${await stageRes.text()}`).toBeTruthy();

  const fresh = await api.getTask(task.id);
  expect(fresh.execution_stage).toBe('in_review');

  await gotoTaskPage(page, task);
  const btn = page.locator('#task-page-content .mark-done-btn');
  await expect(btn).toBeVisible({ timeout: 5_000 });
  await expect(btn).toContainText(/mark done/i);
});

test('Mark done button is absent when execution_stage is not in_review', async ({ page, api }) => {
  const task = await api.createTask('Mark done absent');
  // Task is created in todo stage by default.
  expect(task.execution_stage).toBe('todo');

  await gotoTaskPage(page, task);
  await expect(page.locator('#task-page-content .mark-done-btn')).toHaveCount(0);
});

test('Mark done shows inline error when no work product (409)', async ({ page, api, request }) => {
  const task = await api.createTask('Mark done 409');

  const headers = { Authorization: `Bearer ${process.env.STAYPOINT_API_TOKEN || ''}`, 'Content-Type': 'application/json' };
  await request.post(`/api/tasks/${encodeURIComponent(task.id)}/stage`, {
    headers,
    data: { stage: 'in_review' },
  });

  await gotoTaskPage(page, task);
  const content = page.locator('#task-page-content');
  const btn = content.locator('.mark-done-btn');
  await expect(btn).toBeVisible({ timeout: 5_000 });

  await btn.click();

  // Error div must appear with a human-readable message; button stays enabled for retry.
  const errDiv = content.locator('.mark-done-error');
  await expect(errDiv).toBeVisible({ timeout: 5_000 });
  await expect(errDiv).toContainText(/work product/i);
  await expect(btn).toBeEnabled();
  await expect(btn).toHaveText(/mark done/i);
});
