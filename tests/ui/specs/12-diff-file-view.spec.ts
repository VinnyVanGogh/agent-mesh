import { test, expect, gotoTaskPage } from '../fixtures';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { execFileSync } from 'node:child_process';

const TOKEN = process.env.STAYPOINT_API_TOKEN || '';

// Helper: create a real git repo with two committed files and a checkpoint ref,
// then return the repo path.
function setupGitRepoWithDiff(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'sta550-'));
  const git = (...args: string[]) =>
    execFileSync('git', args, {
      cwd: dir,
      env: {
        ...process.env,
        GIT_AUTHOR_NAME: 'test',
        GIT_AUTHOR_EMAIL: 't@t',
        GIT_COMMITTER_NAME: 'test',
        GIT_COMMITTER_EMAIL: 't@t',
      },
    });

  git('init', '-b', 'main');
  git('config', 'user.email', 't@t');
  git('config', 'user.name', 'test');

  // Initial commit
  fs.writeFileSync(path.join(dir, 'main.go'), 'package main\n\nfunc main() {}\n');
  git('add', 'main.go');
  git('commit', '-m', 'init');

  // Create checkpoint refs pointing at HEAD (pre-run baseline).
  // Both the session-scoped and the global latest refs must exist so that
  // resolveCheckpointRef("") → "refs/staypoint/checkpoints/latest" succeeds.
  const head = execFileSync('git', ['-C', dir, 'rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  git('update-ref', 'refs/staypoint/checkpoints/global/latest', head);
  git('update-ref', 'refs/staypoint/checkpoints/latest', head);

  // Modify main.go to produce a diff
  fs.writeFileSync(path.join(dir, 'main.go'), 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello")\n}\n');
  git('add', 'main.go');
  git('commit', '-m', 'add fmt');

  return dir;
}

test.describe('diff sidebar — file click opens diff modal', () => {
  test('clicking a file in the diff pane shows the changed lines', async ({ page, request }) => {
    const repoPath = setupGitRepoWithDiff();

    // Create task pointing at our real git repo
    const name = `diff-modal-test-${Date.now().toString(36)}`;
    const res = await request.post('/api/tasks', {
      headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' },
      data: JSON.stringify({ name, organization: 'STA', project: 'ui-e2e', repo_path: repoPath }),
    });
    expect(res.ok()).toBeTruthy();
    const task = await res.json();
    expect(task.id).toBeTruthy();

    // The no-worktree diff path compares checkpoint against "staypoint/{taskId}" branch.
    // Create that branch at HEAD so GetTaskDiff and GetTaskFileDiff return actual content.
    execFileSync('git', ['-C', repoPath, 'update-ref', `refs/heads/staypoint/${task.id}`, 'HEAD']);

    await page.goto(`/tasks/STA/ui-e2e/${encodeURIComponent(task.id)}`);
    await expect(page.locator('#task-page-content .task-page-title')).toHaveText(name, { timeout: 20_000 });

    // Wait for diff pane to populate
    const diffRow = page.locator('.diff-file-row').first();
    await expect(diffRow).toBeVisible({ timeout: 10_000 });

    // Click the file name
    await diffRow.locator('.diff-file-name').click();

    // Modal should appear
    const modal = page.locator('.file-diff-modal');
    await expect(modal).toBeVisible({ timeout: 5_000 });

    // The modal should contain the file path
    await expect(modal.locator('.file-diff-header-path')).toContainText('main.go');

    // The diff body should show actual changed lines
    await expect(modal.locator('.ds-add, .ds-del').first()).toBeVisible({ timeout: 5_000 });

    // Escape closes the modal
    await page.keyboard.press('Escape');
    await expect(modal).not.toBeVisible({ timeout: 3_000 });

    fs.rmSync(repoPath, { recursive: true, force: true });
  });

  test('diff endpoint still works after worktree is pruned (whole-run against branch)', async ({
    request,
  }) => {
    const repoPath = setupGitRepoWithDiff();

    const name = `diff-pruned-${Date.now().toString(36)}`;
    const res = await request.post('/api/tasks', {
      headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' },
      data: JSON.stringify({ name, organization: 'STA', project: 'ui-e2e', repo_path: repoPath }),
    });
    expect(res.ok()).toBeTruthy();
    const task = await res.json();
    expect(task.id).toBeTruthy();

    // Create staypoint/{taskId} branch so the no-worktree fallback returns actual content.
    execFileSync('git', ['-C', repoPath, 'update-ref', `refs/heads/staypoint/${task.id}`, 'HEAD']);

    // Hit the file diff endpoint directly (no worktree exists — falls back to branch tip)
    const diffRes = await request.get(`/api/tasks/${encodeURIComponent(task.id)}/diff/file?path=main.go`, {
      headers: { Authorization: `Bearer ${TOKEN}` },
    });
    expect(diffRes.ok()).toBeTruthy();
    const body = await diffRes.json();
    expect(body.path).toBe('main.go');
    expect(typeof body.content).toBe('string');
    expect(body.content).toContain('+');  // non-empty diff with additions
    expect(typeof body.binary).toBe('boolean');
    expect(typeof body.truncated).toBe('boolean');
    expect(['added', 'deleted', 'renamed', 'modified']).toContain(body.status);

    fs.rmSync(repoPath, { recursive: true, force: true });
  });
});
