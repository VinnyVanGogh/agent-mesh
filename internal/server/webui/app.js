/* StayPoint Web UI — vanilla JS, SSE-driven, no build step */
'use strict';

// ── State ────────────────────────────────────────────────
const state = {
  tasks:    {},   // id → task
  sessions: {},   // id → session
  events:   [],   // last N SSE events (boss card stream)
  maxEvents: 200,
};

// ── Token (injected by Go template) ─────────────────────
const TOKEN = document.querySelector('meta[name="staypoint-token"]')?.content || '';

// ── Utils ────────────────────────────────────────────────
function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls)  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function statusPill(status) {
  const p = el('span', `pill pill-${status}`, status.replace('_', ' '));
  return p;
}

function fmtTime(ts) {
  try {
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  } catch { return ''; }
}

function authHeader() {
  return TOKEN ? { 'Authorization': `Bearer ${TOKEN}` } : {};
}

// ── Fetch helpers ─────────────────────────────────────────
async function apiFetch(path) {
  const r = await fetch(path, { headers: authHeader() });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
  return r.json();
}

// ── Initial data load ─────────────────────────────────────
async function loadAll() {
  try {
    const [tasksResp, sessionsResp] = await Promise.all([
      apiFetch('/api/tasks?status=all'),
      apiFetch('/api/sessions'),
    ]);
    for (const t of (tasksResp.tasks || [])) state.tasks[t.id] = t;
    for (const s of (sessionsResp.sessions || [])) state.sessions[s.id] = s;
    renderAll();
  } catch (err) {
    console.error('load failed', err);
  }
}

// ── SSE connection ────────────────────────────────────────
let sseSource = null;
let sseRetryTimer = null;
let sseCursor = null;

function connectSSE() {
  const badge = document.getElementById('conn-badge');
  const url = sseCursor ? `/api/events?cursor=${sseCursor}` : '/api/events';

  if (sseSource) { sseSource.close(); sseSource = null; }

  // Build URL with token if needed
  const fullUrl = TOKEN ? `${url}${url.includes('?') ? '&' : '?'}token=${encodeURIComponent(TOKEN)}` : url;
  sseSource = new EventSource(fullUrl);

  sseSource.onopen = () => {
    badge.className = 'badge badge-live';
    badge.textContent = 'live';
    if (sseRetryTimer) { clearTimeout(sseRetryTimer); sseRetryTimer = null; }
  };

  sseSource.onerror = () => {
    badge.className = 'badge badge-error';
    badge.textContent = 'reconnecting';
    sseSource.close();
    sseRetryTimer = setTimeout(connectSSE, 3000);
  };

  sseSource.onmessage = (ev) => {
    try {
      const evt = JSON.parse(ev.data);
      sseCursor = evt.id ?? sseCursor;
      handleEvent(evt);
    } catch { /* ignore malformed */ }
  };
}

// ── Event dispatch ────────────────────────────────────────
function handleEvent(evt) {
  // Keep event stream for boss card
  state.events.unshift(evt);
  if (state.events.length > state.maxEvents) state.events.length = state.maxEvents;

  const d = evt.data;
  if (!d) { renderAll(); return; }

  switch (evt.type) {
    case 'task_created':
    case 'task_updated':
    case 'task_status_changed':
      if (d.id) state.tasks[d.id] = { ...state.tasks[d.id], ...d };
      renderKanban();
      renderBoss();
      break;

    case 'task_deleted':
      if (d.id) delete state.tasks[d.id];
      renderKanban();
      renderBoss();
      break;

    case 'session_registered':
    case 'session_heartbeat':
      if (d.id) state.sessions[d.id] = { ...state.sessions[d.id], ...d };
      renderFleet();
      break;

    case 'session_closed':
      if (d.id) {
        if (state.sessions[d.id]) state.sessions[d.id].status = 'closed';
      }
      renderFleet();
      break;

    default:
      break;
  }

  renderEventStream();
}

// ── Render: Kanban ────────────────────────────────────────
const KANBAN_COLS = ['todo', 'in_progress', 'blocked', 'done'];

function renderKanban() {
  // Group tasks by status
  const groups = { todo: [], in_progress: [], blocked: [], done: [] };
  for (const t of Object.values(state.tasks)) {
    const col = groups[t.status];
    if (col) col.push(t);
    // soft_deleted → skip
  }

  for (const status of KANBAN_COLS) {
    const list = document.getElementById(`col-${status}`);
    if (!list) continue;
    list.innerHTML = '';
    const tasks = groups[status].sort((a, b) => new Date(b.updated_at || 0) - new Date(a.updated_at || 0));
    for (const t of tasks) {
      list.appendChild(makeTaskCard(t));
    }
  }
}

function makeTaskCard(task) {
  const card = el('div', 'task-card');
  card.dataset.id = task.id;

  const title = el('div', 'card-title', task.title || '(untitled)');
  const meta  = el('div', 'card-meta');

  const dot = el('span', `card-status-dot dot-${task.status}`);
  const id  = el('span', 'card-id', task.id ? `#${task.id.slice(0, 8)}` : '');

  meta.appendChild(dot);
  meta.appendChild(id);
  card.appendChild(title);
  card.appendChild(meta);

  card.addEventListener('click', () => openDetail(task.id));
  return card;
}

// ── Render: Fleet ─────────────────────────────────────────
function renderFleet() {
  const grid = document.getElementById('fleet-grid');
  grid.innerHTML = '';

  const sessions = Object.values(state.sessions);
  if (!sessions.length) {
    grid.appendChild(el('p', null, 'No sessions registered yet.'));
    return;
  }

  sessions.sort((a, b) => new Date(b.last_heartbeat_at || 0) - new Date(a.last_heartbeat_at || 0));
  for (const s of sessions) {
    grid.appendChild(makeFleetCard(s));
  }
}

function makeFleetCard(s) {
  const card = el('div', 'fleet-card');

  const agentLine = el('div', 'fleet-agent', s.agent_type || 'agent');
  const repoLine  = el('div', 'fleet-repo',  `${s.repo_path || '—'} (${s.git_branch || '—'})`);

  const statusRow = el('div', 'fleet-status');
  const dotCls = s.status === 'active' ? 'fleet-dot fleet-active'
               : s.status === 'idle'   ? 'fleet-dot fleet-idle'
               : 'fleet-dot fleet-closed';
  const dot    = el('span', dotCls);
  const stTxt  = el('span', null, s.status || 'unknown');
  const hbTxt  = el('span', null, s.last_heartbeat_at ? ` · last hb ${fmtTime(s.last_heartbeat_at)}` : '');

  statusRow.appendChild(dot);
  statusRow.appendChild(stTxt);
  statusRow.appendChild(hbTxt);

  card.appendChild(agentLine);
  card.appendChild(repoLine);
  card.appendChild(statusRow);
  return card;
}

// ── Render: Boss Card ─────────────────────────────────────
function renderBoss() {
  const container = document.getElementById('boss-container');

  const tasks    = Object.values(state.tasks);
  const todo     = tasks.filter(t => t.status === 'todo').length;
  const inProg   = tasks.filter(t => t.status === 'in_progress').length;
  const blocked  = tasks.filter(t => t.status === 'blocked').length;
  const done     = tasks.filter(t => t.status === 'done').length;
  const total    = tasks.length;

  const sessions = Object.values(state.sessions);
  const active   = sessions.filter(s => s.status === 'active').length;

  container.innerHTML = '';

  const card = el('div', 'boss-card');

  const header = el('div', 'boss-header');
  const titleWrap = el('div');
  titleWrap.appendChild(el('div', 'boss-title', 'StayPoint'));
  titleWrap.appendChild(el('div', 'boss-subtitle', `${total} task${total !== 1 ? 's' : ''} · ${active} session${active !== 1 ? 's' : ''} active`));
  header.appendChild(titleWrap);
  card.appendChild(header);

  const grid = el('div', 'boss-stat-grid');
  const stats = [
    { val: inProg,  label: 'In Progress' },
    { val: todo,    label: 'Todo' },
    { val: blocked, label: 'Blocked' },
    { val: done,    label: 'Done' },
    { val: active,  label: 'Agents' },
  ];
  for (const { val, label } of stats) {
    const s = el('div', 'boss-stat');
    s.appendChild(el('div', 'boss-stat-val', String(val)));
    s.appendChild(el('div', 'boss-stat-label', label));
    grid.appendChild(s);
  }
  card.appendChild(grid);
  container.appendChild(card);

  renderEventStream();
}

function renderEventStream() {
  // Event stream lives inside boss card
  const container = document.getElementById('boss-container');
  if (!container.querySelector('.boss-card')) return;

  let streamEl = container.querySelector('.event-stream');
  if (!streamEl) {
    streamEl = el('div', 'event-stream');
    container.querySelector('.boss-card').appendChild(streamEl);
  }

  // Full re-render of event rows (capped at 50 visible)
  streamEl.innerHTML = '';
  const visible = state.events.slice(0, 50);
  for (const evt of visible) {
    const row = el('div', 'event-row');
    row.appendChild(el('span', 'event-ts',   fmtTime(evt.timestamp)));
    row.appendChild(el('span', 'event-type', evt.type || 'event'));
    const data = typeof evt.data === 'object' ? JSON.stringify(evt.data) : String(evt.data ?? '');
    row.appendChild(el('span', 'event-data', data));
    streamEl.appendChild(row);
  }
}

// ── Detail panel ──────────────────────────────────────────
async function openDetail(taskId) {
  const panel   = document.getElementById('detail-panel');
  const content = document.getElementById('panel-content');

  panel.classList.remove('hidden');
  content.innerHTML = '<p style="color:var(--muted)">Loading…</p>';

  try {
    const task = await apiFetch(`/api/tasks/${taskId}`);
    content.innerHTML = '';

    content.appendChild(el('h2', null, task.title || '(untitled)'));

    const metaRow = el('div', 'meta-row');
    metaRow.appendChild(statusPill(task.status || 'unknown'));
    metaRow.appendChild(el('span', null, `id: ${task.id?.slice(0, 12) || '—'}`));
    content.appendChild(metaRow);

    if (task.description) {
      const desc = el('p', null, task.description);
      desc.style.cssText = 'margin-top:12px;color:var(--text);line-height:1.5;font-size:13px;';
      content.appendChild(desc);
    }

    // Comments
    try {
      const commResp = await apiFetch(`/api/tasks/${taskId}/comments`);
      const comments = commResp.comments || [];
      if (comments.length) {
        const h = el('h3', null, `Comments (${comments.length})`);
        h.style.cssText = 'margin-top:20px;font-size:13px;color:var(--muted);';
        content.appendChild(h);
        for (const c of comments) {
          const row = el('div');
          row.style.cssText = 'margin-top:10px;border-left:2px solid var(--border);padding-left:10px;';
          row.appendChild(el('div', null, c.body || ''));
          row.style.fontSize = '12px';
          content.appendChild(row);
        }
      }
    } catch { /* comments optional */ }

  } catch (err) {
    const p = el('p', null, `Failed to load: ${err.message}`);
    p.style.color = 'var(--red)';
    content.innerHTML = '';
    content.appendChild(p);
  }
}

document.getElementById('panel-close').addEventListener('click', () => {
  document.getElementById('detail-panel').classList.add('hidden');
});

// ── Tab navigation ────────────────────────────────────────
function renderAll() {
  renderKanban();
  renderFleet();
  renderBoss();
}

document.querySelectorAll('.tab').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach(b => b.classList.remove('active'));
    document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
    btn.classList.add('active');
    const view = document.getElementById(`view-${btn.dataset.view}`);
    if (view) view.classList.add('active');
  });
});

// ── Boot ──────────────────────────────────────────────────
loadAll().then(() => connectSSE());
