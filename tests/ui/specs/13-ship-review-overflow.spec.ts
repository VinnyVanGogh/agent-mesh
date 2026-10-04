/**
 * spec 13: Ship review card — no overflow at narrow viewports (STA-585)
 *
 * Renders a ship review card with maximally long content (long URL, long branch
 * name, long test-step commands, double-numbered steps) and asserts at 1024px
 * and 768px that nothing inside `.ship-review-card` exceeds the card's own
 * scrollWidth, and that the double-number strip works.
 *
 * Uses page.route() to inject a synthetic card so no real git repo is needed.
 */

import { test, expect, gotoTaskPage } from '../fixtures';

const LONG_URL = 'http://127.0.0.1:3333/very/long/path/that/keeps/going/and/going/and/going/even/further';
const LONG_BRANCH = 'staypoint/task-4fb4d2a5-very-long-branch-name-that-should-wrap';
const LONG_SHA = 'deadbeef12345678abcdef9900112233deadbeef12345678';

const SYNTHETIC_CARD = {
  id: 'overflow-test-card',
  task_id: 'placeholder',
  branch: LONG_BRANCH,
  head_sha: LONG_SHA,
  test_steps: [
    '1. Checkout the branch and run the dev server',
    '2. python3 -c "print(open(\'/path/to/very/long/file/in/the/repository/config.json\').read())"',
    '3. Verify that the output matches the expected JSON structure with all required fields',
    '4. Run: curl -s http://127.0.0.1:3333/api/health | python3 -m json.tool | grep -i status',
  ],
  dev_url: LONG_URL,
  dev_pid: 0,
  status: 'pending',
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

const VIEWPORTS = [
  { label: '1024px', width: 1024, height: 768 },
  { label: '768px', width: 768, height: 1024 },
];

for (const vp of VIEWPORTS) {
  test(`no overflow in ship review card at ${vp.label}`, async ({ page, api }) => {
    const task = await api.createTask(`sr-overflow-${vp.width}`);
    const card = { ...SYNTHETIC_CARD, task_id: task.id };

    // Intercept the ship-review API so we get a card without a real git branch.
    await page.route(`**/api/tasks/${encodeURIComponent(task.id)}/ship-review`, async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(card) });
      } else {
        await route.continue();
      }
    });

    await page.setViewportSize({ width: vp.width, height: vp.height });
    await gotoTaskPage(page, task);

    const cardLocator = page.locator('.ship-review-card');
    await expect(cardLocator).toBeVisible({ timeout: 12_000 });

    // Assert no child of .ship-review-card overflows its container horizontally.
    const overflowFound = await page.evaluate(() => {
      const card = document.querySelector('.ship-review-card');
      if (!card) return 'card not found';
      const cardRect = card.getBoundingClientRect();
      const cardRight = Math.round(cardRect.right);
      const all = card.querySelectorAll('*');
      for (const el of all) {
        // Skip elements with overflow-x scroll/auto (they contain their own overflow).
        const style = getComputedStyle(el);
        if (style.overflowX === 'auto' || style.overflowX === 'scroll') continue;
        const rect = el.getBoundingClientRect();
        if (Math.round(rect.right) > cardRight + 2) {
          return `${el.tagName}.${el.className} right=${Math.round(rect.right)} > card right=${cardRight}`;
        }
      }
      return null;
    });
    expect(overflowFound, `overflow at ${vp.label}: ${overflowFound}`).toBeNull();

    // Also verify scrollWidth ≤ clientWidth on the card itself.
    const cardOverflow = await cardLocator.evaluate((el) => el.scrollWidth > el.clientWidth + 2);
    expect(cardOverflow, `card scrollWidth > clientWidth at ${vp.label}`).toBe(false);
  });
}

test('test steps strip leading numbering inside ol', async ({ page, api }) => {
  const task = await api.createTask('sr-double-number');
  const card = {
    ...SYNTHETIC_CARD,
    task_id: task.id,
    test_steps: ['1. Checkout the branch', '2) Open homepage', '3. Verify title'],
  };

  await page.route(`**/api/tasks/${encodeURIComponent(task.id)}/ship-review`, async (route) => {
    if (route.request().method() === 'GET') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(card) });
    } else {
      await route.continue();
    }
  });

  await gotoTaskPage(page, task);
  const list = page.locator('.ship-review-test-list');
  await expect(list).toBeVisible({ timeout: 12_000 });

  const items = list.locator('li');
  await expect(items).toHaveCount(3);

  // None of the rendered <li> text should start with a digit followed by . or )
  for (let i = 0; i < 3; i++) {
    const text = await items.nth(i).textContent();
    expect(text, `step ${i} starts with number`).not.toMatch(/^\s*\d+[.)]/);
  }
});
