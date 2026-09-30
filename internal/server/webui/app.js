/* StayPoint Web UI — Multi-Org Fleet Dashboard, SSE-driven, no build step */
'use strict';

// ── State ────────────────────────────────────────────────
const state = {
  tasks:    {},   // id → task
  sessions: {},   // id → session
  fleet:    null, // FleetOverview object
  events:   [],   // last N SSE events (boss card stream)
  maxEvents: 200,
  taskFilter: {
    search: '',
    org: 'all',
    status: 'all',
  },
};

// ── Token (injected by Go template) ─────────────────────
const TOKEN = document.querySelector('meta[name="staypoint-token"]')?.content || '';

// ── Utils ────────────────────────────────────────────────

// Map Paperclip/fleet task status → kanban column
function normalizeFleetStatus(s) {
  if (s === 'running' || s === 'in_progress') return 'in_progress';
  if (s === 'blocked') return 'blocked';
  if (s === 'done' || s === 'completed' || s === 'soft_deleted') return 'done';
  return 'todo'; // active, pending, queued, etc.
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls)  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function statusPill(status) {
  const norm = (status || 'active').toLowerCase().replace(' ', '_');
  const p = el('span', `pill pill-${norm}`, norm.replace('_', ' '));
  return p;
}

function fmtNum(n) {
  if (n === undefined || n === null) return '0';
  return Number(n).toLocaleString();
}

function fmtCompactNum(n) {
  if (!n) return '0';
  const num = Number(n);
  if (num >= 1_000_000_000) return (num / 1_000_000_000).toFixed(2) + 'B';
  if (num >= 1_000_000) return (num / 1_000_000).toFixed(2) + 'M';
  if (num >= 1_000) return (num / 1_000).toFixed(1) + 'k';
  return num.toLocaleString();
}

function fmtCurrency(usd) {
  if (usd === undefined || usd === null) return '$0.00';
  return '$' + Number(usd).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function fmtTime(ts) {
  try {
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  } catch { return ''; }
}

function formatCountdown(targetTs) {
  if (!targetTs) return '';
  const diffMs = new Date(targetTs).getTime() - Date.now();
  if (diffMs <= 0) return 'resets soon';
  const mins = Math.floor(diffMs / 60000);
  const hrs = Math.floor(mins / 60);
  const remMins = mins % 60;
  if (hrs > 0) return `resets in ${hrs}h ${remMins}m`;
  return `resets in ${remMins}m`;
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
    const [fleetResp, tasksResp, sessionsResp] = await Promise.all([
      apiFetch('/api/fleet/overview').catch(() => null),
      apiFetch('/api/tasks?status=all').catch(() => ({ tasks: [] })),
      apiFetch('/api/sessions').catch(() => ({ sessions: [] })),
    ]);

    if (fleetResp) state.fleet = fleetResp;
    for (const t of (tasksResp.tasks || [])) state.tasks[t.id] = t;
    for (const s of (sessionsResp.sessions || [])) state.sessions[s.id] = s;

    // Merge fleet (Paperclip) tasks into state.tasks with normalized status so
    // the kanban and detail panel can render them without a separate API call.
    if (fleetResp?.tasks) {
      for (const t of fleetResp.tasks) {
        // Don't overwrite a local task that happens to share an ID
        if (!state.tasks[t.id]) {
          state.tasks[t.id] = { ...t, status: normalizeFleetStatus(t.status) };
        }
      }
    }

    populateOrgFilter();
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
  state.events.unshift(evt);
  if (state.events.length > state.maxEvents) state.events.pop();

  const type = evt.type || '';
  if (type.startsWith('task.') && evt.data) {
    const t = evt.data;
    if (t.id) state.tasks[t.id] = Object.assign(state.tasks[t.id] || {}, t);
    refreshFleetData();
  } else if (type.startsWith('session.') && evt.data) {
    const s = evt.data;
    if (s.id) state.sessions[s.id] = Object.assign(state.sessions[s.id] || {}, s);
    refreshFleetData();
  } else if (type.startsWith('quota.') || type.startsWith('fleet.')) {
    refreshFleetData();
  }

  renderAll();
}

async function refreshFleetData() {
  try {
    const fleetResp = await apiFetch('/api/fleet/overview');
    if (fleetResp) {
      state.fleet = fleetResp;
      populateOrgFilter();
      renderOverview();
    }
  } catch { /* silent retry on next cycle */ }
}

// ── Render: All Organizations Overview Screen ─────────────
function renderOverview() {
  const f = state.fleet;
  if (!f) return;

  // 1. Top KPI Summary Cards
  const orgCount = (f.organizations || []).length;
  document.getElementById('kpi-orgs').textContent = String(orgCount);

  const runningTasks = f.global_tasks?.running || 0;
  const totalTasks = f.global_tasks?.total || 0;
  document.getElementById('kpi-running-tasks').textContent = String(runningTasks);
  document.getElementById('kpi-tasks-total').textContent = `${totalTasks} total tasks`;

  const activeAgents = f.global_agents?.active_running || 0;
  document.getElementById('kpi-active-agents').textContent = String(activeAgents);

  const providerCounts = f.global_agents?.by_provider || {};
  document.getElementById('kpi-agents-breakdown').textContent =
    `Claude: ${providerCounts.claude || 0} · Gemini: ${providerCounts.gemini || 0} · OpenAI: ${providerCounts.openai || 0}`;

  const totTokens = f.token_telemetry?.total_tokens || 0;
  document.getElementById('kpi-total-tokens').textContent = fmtCompactNum(totTokens);
  document.getElementById('kpi-tokens-io').textContent =
    `In: ${fmtCompactNum(f.token_telemetry?.input_tokens)} · Out: ${fmtCompactNum(f.token_telemetry?.output_tokens)}`;

  const totCost = f.token_telemetry?.total_cost_usd || 0;
  document.getElementById('kpi-total-cost').textContent = fmtCurrency(totCost);

  // Pacing status badge in header
  updateFleetPacingBadge(f.provider_quotas);

  // 2. 5-Hour Rolling Quotas & Lockout Gauges Grid
  renderQuotaGauges(f.provider_quotas);

  // 3. Organizations Grid & Running Agent Counts
  renderOrganizationsGrid(f.organizations);

  // 4. Token Telemetry & Cost Accounting
  renderTokenTelemetrySection(f.token_telemetry, f.model_spend, f.org_spend);

  // 5. Global Task Table
  renderGlobalTaskTable();
}

function updateFleetPacingBadge(quotas) {
  const pill = document.getElementById('pacing-pill');
  if (!pill || !quotas) return;

  let anyLocked = false;
  let anyOverpaced = false;

  for (const q of Object.values(quotas)) {
    if (q.is_locked || q.projection_status === 'locked_out') anyLocked = true;
    else if (q.projection_status === 'overpaced') anyOverpaced = true;
  }

  if (anyLocked) {
    pill.className = 'pill pill-red';
    pill.textContent = '✖ Quota Lockout Detected';
  } else if (anyOverpaced) {
    pill.className = 'pill pill-amber';
    pill.textContent = '⚠ Overpaced Burn Warning';
  } else {
    pill.className = 'pill pill-green';
    pill.textContent = '✔ Fleet Pacing On Track';
  }
}

function renderQuotaGauges(quotas) {
  const grid = document.getElementById('quota-gauges-grid');
  if (!grid || !quotas) return;
  grid.innerHTML = '';

  const order = ['gemini', 'claude', 'openai'];
  for (const key of order) {
    const q = quotas[key];
    if (!q) continue;

    const card = el('div', 'gauge-card');

    // Header
    const hdr = el('div', 'gauge-card-header');
    hdr.appendChild(el('span', 'gauge-provider-name', q.display_name || key));

    let statusPillClass = 'pill-green';
    let statusText = '✔ On Track';
    if (q.is_locked || q.projection_status === 'locked_out') {
      statusPillClass = 'pill-red';
      statusText = '✖ Locked Out';
    } else if (q.projection_status === 'overpaced') {
      statusPillClass = 'pill-amber';
      statusText = '⚠ Overpaced';
    }
    const statusEl = el('span', `pill ${statusPillClass}`, statusText);
    hdr.appendChild(statusEl);
    card.appendChild(hdr);

    // 5-Hour Progress Bar
    const remainingPct = Math.max(0, Math.min(100, q.five_hour_remaining_pct ?? 100));
    const usedPct = 100 - remainingPct;

    const barOuter = el('div', 'gauge-bar-outer');
    const barInner = el('div', 'gauge-bar-inner');
    barInner.style.width = `${usedPct}%`;

    if (q.is_locked || usedPct >= 95) {
      barInner.className = 'gauge-bar-inner gauge-bar-red';
    } else if (usedPct >= 75) {
      barInner.className = 'gauge-bar-inner gauge-bar-amber';
    } else {
      barInner.className = 'gauge-bar-inner gauge-bar-green';
    }
    barOuter.appendChild(barInner);
    card.appendChild(barOuter);

    // Metrics Row
    const mRow = el('div', 'gauge-metrics-row');
    const resetStr = formatCountdown(q.five_hour_resets_at);
    mRow.appendChild(el('span', null, `5h Used: ${usedPct.toFixed(1)}% (${remainingPct.toFixed(1)}% Left)`));
    mRow.appendChild(el('span', 'gauge-metric-val', resetStr || 'rolling window'));
    card.appendChild(mRow);

    // Burn rate and Lockout threshold
    const bRow = el('div', 'gauge-metrics-row');
    bRow.appendChild(el('span', null, `Burn Rate: ${q.burn_rate_5h ? q.burn_rate_5h.toFixed(2) + '%/turn' : '0.0%/turn'}`));
    bRow.appendChild(el('span', null, `Lockout Limit: ${q.lockout_threshold_pct || 100}%`));
    card.appendChild(bRow);

    // Weekly pacing projection box
    const projBox = el('div', 'gauge-projection-box');
    projBox.textContent = q.projection_message || 'Sustainable pacing against weekly budget';
    card.appendChild(projBox);

    grid.appendChild(card);
  }
}

function renderOrganizationsGrid(orgs) {
  const container = document.getElementById('orgs-grid');
  if (!container || !orgs) return;
  container.innerHTML = '';

  for (const org of orgs) {
    const card = el('div', 'org-card');

    const top = el('div', 'org-card-title');
    top.appendChild(el('span', 'org-name', org.name));
    top.appendChild(el('span', 'org-prefix', `[${org.issue_prefix || 'ORG'}]`));
    card.appendChild(top);

    // Stats Grid
    const sGrid = el('div', 'org-stats-grid');

    const running = el('div');
    running.appendChild(el('div', 'org-stat-n highlight-cyan', String(org.task_counts?.running || 0)));
    running.appendChild(el('div', 'org-stat-l', 'Running'));
    sGrid.appendChild(running);

    const activeAg = el('div');
    activeAg.appendChild(el('div', 'org-stat-n highlight-green', String(org.active_agents || 0)));
    activeAg.appendChild(el('div', 'org-stat-l', 'Agents'));
    sGrid.appendChild(activeAg);

    const blocked = el('div');
    blocked.appendChild(el('div', 'org-stat-n highlight-red', String(org.task_counts?.blocked || 0)));
    blocked.appendChild(el('div', 'org-stat-l', 'Blocked'));
    sGrid.appendChild(blocked);

    card.appendChild(sGrid);

    // Provider Breakdown row
    const provs = org.active_agents_by_provider || {};
    const provText = `Claude: ${provs.claude || 0} · Gemini: ${provs.gemini || 0} · OpenAI: ${provs.openai || 0}`;
    const provRow = el('div', 'gauge-metrics-row');
    provRow.appendChild(el('span', null, 'Active Deployments:'));
    provRow.appendChild(el('span', 'gauge-metric-val', provText));
    card.appendChild(provRow);

    // Token Spend summary row
    const spendRow = el('div', 'gauge-metrics-row');
    spendRow.appendChild(el('span', null, `Spend: ${fmtCurrency(org.spent_usd)}`));
    spendRow.appendChild(el('span', null, `Tokens: ${fmtCompactNum(org.spent_tokens)}`));
    card.appendChild(spendRow);

    container.appendChild(card);
  }
}

function renderTokenTelemetrySection(telemetry, modelSpend, orgSpend) {
  const aggCol = document.getElementById('telemetry-aggregates');
  const spCol = document.getElementById('telemetry-spend-breakdown');
  if (!aggCol || !spCol) return;

  aggCol.innerHTML = '';
  spCol.innerHTML = '';

  // Aggregation side
  aggCol.appendChild(el('h3', 'section-title', 'Global Token Consumption'));

  const aggRows = [
    { label: 'Total Tokens Ingested', val: fmtNum(telemetry?.total_tokens) },
    { label: 'Prompt Input Tokens', val: fmtNum(telemetry?.input_tokens) },
    { label: 'Completion Output Tokens', val: fmtNum(telemetry?.output_tokens) },
    { label: 'Cache Read / Reused Tokens', val: fmtNum(telemetry?.cache_read_tokens) },
    { label: 'Total API List-Price Spend', val: fmtCurrency(telemetry?.total_cost_usd) },
  ];

  for (const r of aggRows) {
    const row = el('div', 'telemetry-item-row');
    row.appendChild(el('span', null, r.label));
    row.appendChild(el('span', 'gauge-metric-val', r.val));
    aggCol.appendChild(row);
  }

  // Spend breakdown side
  spCol.appendChild(el('h3', 'section-title', 'Spend Breakdown by Model'));

  const topModels = (modelSpend || []).slice(0, 5);
  if (!topModels.length) {
    spCol.appendChild(el('p', null, 'No granular model telemetry recorded yet.'));
  } else {
    for (const m of topModels) {
      const bRow = el('div', 'breakdown-row');

      const hdr = el('div', 'breakdown-header');
      hdr.appendChild(el('span', null, m.model));
      hdr.appendChild(el('span', null, `${fmtCurrency(m.cost_usd)} (${m.percentage || 0}%)`));
      bRow.appendChild(hdr);

      const bOuter = el('div', 'breakdown-bar-outer');
      const bInner = el('div', 'breakdown-bar-inner');
      bInner.style.width = `${Math.min(100, m.percentage || 0)}%`;
      bOuter.appendChild(bInner);
      bRow.appendChild(bOuter);

      spCol.appendChild(bRow);
    }
  }

  // Org Spend Allocation
  spCol.appendChild(el('h3', 'section-title', 'Spend by Organization'));
  for (const o of (orgSpend || [])) {
    const row = el('div', 'telemetry-item-row');
    row.appendChild(el('span', null, o.organization));
    row.appendChild(el('span', 'gauge-metric-val', `${fmtCurrency(o.cost_usd)} (${o.percentage || 0}%)`));
    spCol.appendChild(row);
  }
}

function populateOrgFilter() {
  const select = document.getElementById('task-org-filter');
  if (!select || !state.fleet?.organizations) return;

  const current = select.value;
  select.innerHTML = '<option value="all">All Organizations</option>';

  for (const org of state.fleet.organizations) {
    const opt = document.createElement('option');
    opt.value = org.name;
    opt.textContent = org.name;
    select.appendChild(opt);
  }
  select.value = current || 'all';
}

function renderGlobalTaskTable() {
  const tbody = document.getElementById('global-task-tbody');
  if (!tbody) return;
  tbody.innerHTML = '';

  let tasks = state.fleet?.tasks || [];
  if (!tasks.length) {
    tasks = Object.values(state.tasks);
  }

  // Apply search and filter
  const sTerm = (state.taskFilter.search || '').toLowerCase().trim();
  const orgFilter = state.taskFilter.org || 'all';
  const statusFilter = state.taskFilter.status || 'all';

  const filtered = tasks.filter(t => {
    if (orgFilter !== 'all' && t.organization !== orgFilter) return false;
    if (statusFilter !== 'all') {
      const st = (t.status || 'active').toLowerCase();
      if (statusFilter === 'running' && st !== 'running' && st !== 'in_progress') return false;
      if (statusFilter === 'active' && st !== 'active' && st !== 'todo') return false;
      if (statusFilter === 'blocked' && st !== 'blocked' && !t.is_blocked) return false;
      if (statusFilter === 'stopped' && st !== 'stopped' && st !== 'cancelled' && st !== 'paused') return false;
      if (statusFilter === 'errored' && st !== 'errored' && st !== 'error' && st !== 'failed') return false;
      if (statusFilter === 'done' && st !== 'done') return false;
    }
    if (sTerm) {
      const matchTitle = (t.title || '').toLowerCase().includes(sTerm);
      const matchId = (t.identifier || t.id || '').toLowerCase().includes(sTerm);
      const matchOrg = (t.organization || '').toLowerCase().includes(sTerm);
      if (!matchTitle && !matchId && !matchOrg) return false;
    }
    return true;
  });

  if (!filtered.length) {
    const tr = document.createElement('tr');
    const td = document.createElement('td');
    td.colSpan = 7;
    td.textContent = 'No tasks match current filter criteria.';
    td.style.textAlign = 'center';
    td.style.color = 'var(--muted)';
    td.style.padding = '24px';
    tr.appendChild(td);
    tbody.appendChild(tr);
    return;
  }

  for (const t of filtered) {
    const tr = document.createElement('tr');

    const tdId = el('td', null, t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : '—'));
    tdId.style.fontFamily = 'monospace';
    tdId.style.fontWeight = '600';

    const tdTitle = el('td', null, t.title || '(untitled)');
    const tdOrg   = el('td', null, t.organization || 'StayPoint');
    const tdStat  = el('td', null);
    tdStat.appendChild(statusPill(t.status));

    const tdPri   = el('td', null, t.priority || 'medium');
    const tdSpend = el('td', null, `${fmtCurrency(t.spent_usd)} (${fmtCompactNum(t.spent_tokens)} tok)`);
    const tdUp    = el('td', null, fmtTime(t.updated_at || Date.now()));

    tr.appendChild(tdId);
    tr.appendChild(tdTitle);
    tr.appendChild(tdOrg);
    tr.appendChild(tdStat);
    tr.appendChild(tdPri);
    tr.appendChild(tdSpend);
    tr.appendChild(tdUp);

    tr.addEventListener('click', () => openDetail(t.id));
    tbody.appendChild(tr);
  }
}

// ── Render: Kanban ────────────────────────────────────────
const KANBAN_COLS = ['todo', 'in_progress', 'blocked', 'done'];

function renderKanban() {
  const groups = { todo: [], in_progress: [], blocked: [], done: [] };
  const allTasks = Object.values(state.tasks);

  for (const t of allTasks) {
    const col = groups[t.status];
    if (col) col.push(t);
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
  const id  = el('span', 'card-id', task.identifier || (task.id ? `#${task.id.slice(0, 8)}` : ''));

  meta.appendChild(dot);
  meta.appendChild(id);
  card.appendChild(title);
  card.appendChild(meta);

  card.addEventListener('click', () => openDetail(task.id));
  return card;
}

// ── Render: Fleet (Sessions) ──────────────────────────────
function renderFleet() {
  const grid = document.getElementById('fleet-grid');
  if (!grid) return;
  grid.innerHTML = '';

  const sessions = Object.values(state.sessions);
  if (!sessions.length) {
    grid.appendChild(el('p', null, 'No agent sessions registered yet.'));
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
  if (!container) return;

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
  titleWrap.appendChild(el('div', 'boss-title', 'StayPoint Fleet'));
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
  const container = document.getElementById('boss-container');
  if (!container || !container.querySelector('.boss-card')) return;

  let streamEl = container.querySelector('.event-stream');
  if (!streamEl) {
    streamEl = el('div', 'event-stream');
    container.querySelector('.boss-card').appendChild(streamEl);
  }

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
function renderDetailContent(content, task) {
  content.innerHTML = '';
  content.appendChild(el('h2', null, task.title || '(untitled)'));

  const metaRow = el('div', 'meta-row');
  metaRow.appendChild(statusPill(task.status || 'unknown'));
  if (task.identifier) metaRow.appendChild(el('span', 'card-id', task.identifier));
  if (task.priority)   metaRow.appendChild(statusPill(task.priority));
  if (task.organization) metaRow.appendChild(el('span', 'pill', task.organization));
  metaRow.appendChild(el('span', 'muted-text', `id: ${task.id?.slice(0, 12) || '—'}`));
  content.appendChild(metaRow);

  if (task.description) {
    const desc = el('p', null, task.description);
    desc.style.cssText = 'margin-top:12px;color:var(--text);line-height:1.5;font-size:13px;';
    content.appendChild(desc);
  }

  if (task.spent_usd || task.spent_tokens) {
    const spend = el('p', null, `Spend: ${fmtCurrency(task.spent_usd)} · ${fmtCompactNum(task.spent_tokens)} tokens`);
    spend.style.cssText = 'margin-top:8px;font-size:12px;color:var(--muted);';
    content.appendChild(spend);
  }
}

async function openDetail(taskId) {
  const panel   = document.getElementById('detail-panel');
  const content = document.getElementById('panel-content');

  panel.classList.remove('hidden');
  content.innerHTML = '<p style="color:var(--muted)">Loading…</p>';

  // Fleet (Paperclip) tasks are already in state.tasks after merge in loadAll.
  // Try the local API first for full data; fall back to cached state on 404.
  try {
    const task = await apiFetch(`/api/tasks/${taskId}`);
    renderDetailContent(content, task);

    try {
      const commResp = await apiFetch(`/api/tasks/${taskId}/comments`);
      const comments = commResp.comments || [];
      if (comments.length) {
        const h = el('h3', null, `Comments (${comments.length})`);
        h.style.cssText = 'margin-top:20px;font-size:13px;color:var(--muted);';
        content.appendChild(h);
        for (const c of comments) {
          const row = el('div');
          row.style.cssText = 'margin-top:10px;border-left:2px solid var(--border);padding-left:10px;font-size:12px;';
          row.appendChild(el('div', null, c.body || ''));
          content.appendChild(row);
        }
      }
    } catch { /* comments optional */ }

  } catch (err) {
    const cached = state.tasks[taskId];
    if (cached) {
      renderDetailContent(content, cached);
    } else {
      const p = el('p', null, `Failed to load: ${err.message}`);
      p.style.color = 'var(--red)';
      content.innerHTML = '';
      content.appendChild(p);
    }
  }
}

document.getElementById('panel-close').addEventListener('click', () => {
  document.getElementById('detail-panel').classList.add('hidden');
});

// ── Tab navigation ────────────────────────────────────────
function renderAll() {
  renderOverview();
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

// ── Filter listeners ──────────────────────────────────────
const searchInput = document.getElementById('task-search-input');
if (searchInput) {
  searchInput.addEventListener('input', (e) => {
    state.taskFilter.search = e.target.value;
    renderGlobalTaskTable();
  });
}

const orgSelect = document.getElementById('task-org-filter');
if (orgSelect) {
  orgSelect.addEventListener('change', (e) => {
    state.taskFilter.org = e.target.value;
    renderGlobalTaskTable();
  });
}

const statusSelect = document.getElementById('task-status-filter');
if (statusSelect) {
  statusSelect.addEventListener('change', (e) => {
    state.taskFilter.status = e.target.value;
    renderGlobalTaskTable();
  });
}

// ── Boot ──────────────────────────────────────────────────
loadAll().then(() => connectSSE());
