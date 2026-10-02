import { test, expect, knownBug } from '../fixtures';

test.describe('task list', () => {
  test('loads and shows a task seeded over the API', async ({ page, api }) => {
    const task = await api.createTask('List seeded');
    await page.goto('/task-status');
    const table = page.locator('#ts-task-table');
    await expect(table).toBeVisible();
    await expect(table.getByText(task.name)).toBeVisible();
  });

  test('task created over the API appears without a reload (live SSE)', async ({ page, api }) => {
    knownBug('STA-377');
    await page.goto('/task-status');
    await expect(page.locator('#conn-badge')).toHaveText('live');
    const task = await api.createTask('List live');
    await expect(page.locator('#ts-task-table').getByText(task.name)).toBeVisible();
  });

  test('create a task with the New Task form', async ({ page, api }) => {
    await page.goto('/task-status');
    await page.getByRole('button', { name: '+ New Task' }).click({ timeout: 5_000 });
    const name = `UI created ${Date.now().toString(36)}`;
    await page.locator('#ct-name').fill(name);
    await page.locator('#ct-org').fill('STA');
    await page.locator('#ct-project').fill('ui-e2e');
    await page.locator('#create-task-submit').click();
    await expect(page.locator('#create-task-modal')).toBeHidden();
    await expect(page.locator('#ts-task-table').getByText(name)).toBeVisible();
  });
});
