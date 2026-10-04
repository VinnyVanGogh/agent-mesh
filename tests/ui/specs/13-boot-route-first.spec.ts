import { test, expect, gotoTaskPage } from '../fixtures';

// STA-576: Refreshing a task URL must not flash the All Organizations view.
// The task page title must be visible within 1 s and the overview view must
// never be the active view during the load.
test.describe('boot route-first (STA-576)', () => {
  test('task page visible within 1 s on direct navigation; All Organizations never shown', async ({
    page,
    api,
  }) => {
    const task = await api.createTask('BootFlash');

    // Navigate once so the page is warm (avoids first-time server startup noise).
    await gotoTaskPage(page, task);

    // Now reload the task URL directly – this is the regression scenario.
    const reloadStart = Date.now();
    await page.reload({ waitUntil: 'domcontentloaded' });

    // The task page title must appear within 1 s of DOMContentLoaded.
    const title = page.locator('#task-page-content .task-page-title');
    await expect(title).toBeVisible({ timeout: 1_000 });

    const ttfb = Date.now() - reloadStart;
    console.log(`[STA-576] task page title visible in ${ttfb} ms`);

    // The overview / All Organizations view must never become the active view.
    await expect(page.locator('#view-overview')).not.toHaveClass(/\bactive\b/);

    // The task page view must be active.
    await expect(page.locator('#view-task-page')).toHaveClass(/\bactive\b/);
  });

  test('/api/fleet/overview is served from cache on second request', async ({ request }) => {
    const headers = { Authorization: `Bearer ${process.env.STAYPOINT_API_TOKEN || ''}` };

    const t0 = Date.now();
    const r1 = await request.get('/api/fleet/overview', { headers });
    const firstMs = Date.now() - t0;
    expect(r1.ok()).toBe(true);
    console.log(`[STA-576] fleet/overview first call: ${firstMs} ms (${r1.headers()['x-fleet-cache']})`);

    const t1 = Date.now();
    const r2 = await request.get('/api/fleet/overview', { headers });
    const secondMs = Date.now() - t1;
    expect(r2.ok()).toBe(true);
    console.log(`[STA-576] fleet/overview second call: ${secondMs} ms (${r2.headers()['x-fleet-cache']})`);

    // Second call must be a cache hit and complete in under 100 ms.
    expect(r2.headers()['x-fleet-cache']).toBe('hit');
    expect(secondMs).toBeLessThan(100);
  });
});
