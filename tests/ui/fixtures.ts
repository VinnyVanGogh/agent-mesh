import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';

// Everything here talks to the throwaway daemon started by scripts/ui-e2e.sh.
// Fixtures are created over HTTP; the browser is only used for what a person
// would do in the UI.

const TOKEN = process.env.STAYPOINT_API_TOKEN || '';
if (TOKEN.length < 16) {
  throw new Error('STAYPOINT_API_TOKEN is not set: run the suite via scripts/ui-e2e.sh');
}

export type Task = {
  id: string;
  name: string;
  status: string;
  execution_stage: string;
  organization?: string;
  project?: string;
};

export type Interaction = {
  id: number;
  task_id: string;
  interaction_kind: string;
  payload: string;
  status: string;
  response?: unknown;
};

export class StayPointAPI {
  constructor(private readonly request: APIRequestContext) {}

  private headers() {
    return { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' };
  }

  private async json<T>(method: string, path: string, data?: unknown): Promise<T> {
    const res = await this.request.fetch(path, { method, headers: this.headers(), data });
    const text = await res.text();
    if (!res.ok()) {
      throw new Error(`${method} ${path} -> ${res.status()}: ${text}`);
    }
    return (text ? JSON.parse(text) : {}) as T;
  }

  /** Creates a local task with a unique name so specs never collide. */
  async createTask(label: string, extra: Record<string, unknown> = {}): Promise<Task> {
    const name = `${label} ${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
    const body = { name, organization: 'STA', project: 'ui-e2e', ...extra };
    const created = await this.json<Task>('POST', '/api/tasks', body);
    // The create response omits organization/project; keep what was sent so
    // taskPagePath() builds the same URL the UI does.
    return { ...created, organization: body.organization, project: body.project };
  }

  async getTask(id: string): Promise<Task> {
    const r = await this.json<{ task: Task }>('GET', `/api/tasks/${encodeURIComponent(id)}`);
    return r.task;
  }

  async addComment(id: string, body: string) {
    return this.json('POST', `/api/tasks/${encodeURIComponent(id)}/comments`, { body, author: 'ui-e2e' });
  }

  async listComments(id: string): Promise<Array<{ message?: string; body?: string }>> {
    const r = await this.json<{ comments: Array<{ message?: string; body?: string }> | null }>(
      'GET',
      `/api/tasks/${encodeURIComponent(id)}/comments`,
    );
    return r.comments || [];
  }

  /** payload is an object here; the API stores it as a JSON string. */
  async createInteraction(id: string, kind: string, payload: unknown): Promise<Interaction> {
    return this.json<Interaction>('POST', `/api/tasks/${encodeURIComponent(id)}/interactions`, {
      kind,
      payload: JSON.stringify(payload),
    });
  }

  async listInteractions(id: string): Promise<Interaction[]> {
    const r = await this.json<{ interactions: Interaction[] | null }>(
      'GET',
      `/api/tasks/${encodeURIComponent(id)}/interactions`,
    );
    return r.interactions || [];
  }
}

/** URL of the full task page, matching taskToPath() in app.js for local tasks. */
export function taskPagePath(task: Task): string {
  return `/tasks/${encodeURIComponent(task.organization || 'STA')}/${encodeURIComponent(
    task.project || 'default',
  )}/${encodeURIComponent(task.id)}`;
}

/**
 * Opens the full task page and waits until it has rendered. Under CPU
 * contention the first render can take several seconds, so this waits longer
 * than the default expect timeout before specs assert anything on the page.
 */
export async function gotoTaskPage(page: Page, task: Task) {
  await page.goto(taskPagePath(task));
  await expect(page.locator('#task-page-content .task-page-title')).toHaveText(task.name, { timeout: 20_000 });
}

/**
 * Runs tests/ui/stepsim against the throwaway DB: the real StepRecorder
 * writing a short run for the task. Returns how many steps it persisted.
 */
export function simulateRunSteps(taskId: string): number {
  const bin = process.env.STAYPOINT_UI_STEPSIM;
  const db = process.env.STAYPOINT_UI_DB;
  if (!bin || !db) throw new Error('STAYPOINT_UI_STEPSIM / STAYPOINT_UI_DB not set: run via scripts/ui-e2e.sh');
  const out = execFileSync(bin, ['--db', db, '--task', taskId], { encoding: 'utf8' });
  const m = out.match(/^STEPS (\d+)$/m);
  return m ? Number(m[1]) : 0;
}

/**
 * Product bugs the suite already knows about. A spec that hits one is marked
 * as expected-to-fail, so the suite stays green while the bug is open and
 * turns red the moment the bug is fixed (an "unexpected pass"), which is the
 * cue to delete the entry. STAYPOINT_UI_STRICT=1 ignores this list.
 */
export const KNOWN_BUGS = {
  'STA-350': 'Interaction card Accept/Reject send GET instead of POST; question options are not selectable (fix on paperclip/sta-289-v2, not in main)',
  'STA-347': 'No create-task form in the web UI (form on paperclip/sta-289-v2, not in main)',
  'RUN-NOW-ACTIVE': 'Run Now is hidden for local tasks: they have status "active", which is not in runableStatuses (fix on paperclip/sta-289-v2, not in main)',
  'SSE-NAMED-EVENTS': 'Daemon sends named SSE events (event: task_created / run.step) but app.js only listens on onmessage, so the UI never live-updates',
  'RUN-STEPS-SCHEMA': 'StepRecorder inserts run_steps.status and expects an integer id, but the run_steps table has no status column and a TEXT id: every step insert fails',
  'STALE-ON-DAEMON-DOWN': 'With the daemon down, opening a task renders the copy cached at page load with no error or staleness notice (openTaskPage falls back to state.tasks)',
} as const;

export type KnownBug = keyof typeof KNOWN_BUGS;

export function knownBug(id: KnownBug) {
  if (process.env.STAYPOINT_UI_STRICT === '1') return;
  base.info().annotations.push({ type: 'known-bug', description: `${id}: ${KNOWN_BUGS[id]}` });
  base.fail(true, `${id}: ${KNOWN_BUGS[id]}`);
}

type Fixtures = { api: StayPointAPI; page: Page };

export const test = base.extend<Fixtures>({
  api: async ({ request }, use) => {
    await use(new StayPointAPI(request));
  },
  // Log in the way a person does: the first visit carries ?token=, the daemon
  // swaps it for a session cookie and redirects to the clean URL.
  page: async ({ page, baseURL }, use) => {
    await page.goto(`${baseURL}/?token=${encodeURIComponent(TOKEN)}`);
    await expect(page).toHaveURL(`${baseURL}/`);
    await use(page);
  },
});

export { expect };
