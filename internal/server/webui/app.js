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
  currentOrgDetail: null, // currently open org name in org-detail view
};

// ── Token (injected by Go template) ─────────────────────
const TOKEN = document.querySelector('meta[name="staypoint-token"]')?.content || '';

// ── Utils ────────────────────────────────────────────────

function normalizeFleetStatus(s) {
  if (s === 'running' || s === 'in_progress') return 'in_progress';
  if (s === 'blocked') return 'blocked';
  if (s === 'done' || s === 'completed' || s === 'soft_deleted') return 'done';
  return 'todo';
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls)  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function statusPill(status) {
  const norm = (status || 'active').toLowerCase().replace(/\s+/g, '_');
  return el('span', `pill pill-${norm}`, norm.replace(/_/g, ' '));
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
  try { return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }); }
  catch { return ''; }
}

function fmtDateTime(ts) {
  try { return new Date(ts).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }); }
  catch { return ''; }
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

// ── Simple markdown renderer ──────────────────────────────
function escapeHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function renderMarkdown(text) {
  if (!text) return '';
  // Escape HTML first, then apply markdown transformations.
  let s = escapeHtml(text);

  // Fenced code blocks (``` ... ```)
  s = s.replace(/```[\w]*\n?([\s\S]*?)```/g, (_, code) =>
    `<pre><code>${code.trimEnd()}</code></pre>`);

  // Inline code
  s = s.replace(/`([^`\n]+)`/g, '<code>$1</code>');

  // Headers
  s = s.replace(/^### (.+)$/gm, '<h3>$1</h3>');
  s = s.replace(/^## (.+)$/gm, '<h2>$1</h2>');
  s = s.replace(/^# (.+)$/gm, '<h1>$1</h1>');

  // Bold + italic
  s = s.replace(/\*\*\*([^*]+)\*\*\*/g, '<strong><em>$1</em></strong>');
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/\*([^*\n]+)\*/g, '<em>$1</em>');

  // Blockquote
  s = s.replace(/^&gt; (.+)$/gm, '<blockquote>$1</blockquote>');

  // Unordered list items
  s = s.replace(/^[-*] (.+)$/gm, '<li>$1</li>');
  // Wrap consecutive <li> in <ul>
  s = s.replace(/(<li>[\s\S]*?<\/li>)(\n<li>[\s\S]*?<\/li>)*/g, m => `<ul>${m}</ul>`);

  // Ordered list items
  s = s.replace(/^\d+\. (.+)$/gm, '<li>$1</li>');

  // Horizontal rule
  s = s.replace(/^---+$/gm, '<hr>');

  // Paragraphs: split on double newlines, wrap non-block lines in <p>
  const lines = s.split(/\n\n+/);
  const wrapped = lines.map(chunk => {
    chunk = chunk.trim();
    if (!chunk) return '';
    if (/^<(h[1-6]|ul|ol|pre|blockquote|hr)/.test(chunk)) return chunk;
    return `<p>${chunk.replace(/\n/g, '<br>')}</p>`;
  });
  return wrapped.join('\n');
}

function mdEl(text) {
  const div = el('div', 'md-body');
  div.innerHTML = renderMarkdown(text || '');
  return div;
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

    // Merge fleet tasks into state.tasks with normalized status
    if (fleetResp?.tasks) {
      for (const t of fleetResp.tasks) {
        if (!state.tasks[t.id]) {
          state.tasks[t.id] = { ...t, status: normalizeFleetStatus(t.status) };
        }
      }
    }

    populateOrgFilter();
    renderAll();
    renderSidebarOrgTree();
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
      renderSidebarOrgTree();
    }
  } catch { /* silent retry */ }
}

// ── Sidebar ────────────────────────────────────────────────
function renderSidebarOrgTree() {
  const tree = document.getElementById('sidebar-org-tree');
  if (!tree) return;
  tree.innerHTML = '';

  const orgs = state.fleet?.organizations || [];
  if (!orgs.length) {
    tree.appendChild(el('div', 'sidebar-org-placeholder', 'No organizations'));
    return;
  }

  for (const org of orgs) {
    const btn = el('button', 'sidebar-org-item');
    if (state.currentOrgDetail === org.name) btn.classList.add('active');

    const dot = el('span', 'sidebar-org-dot');
    const running = org.task_counts?.running || 0;
    const blocked = org.task_counts?.blocked || 0;
    if (running > 0) dot.classList.add('has-running');
    else if (blocked > 0) dot.classList.add('has-blocked');

    const nameSpan = el('span', null, org.name);
    const countSpan = el('span', 'muted-text', ` (${running})`);

    btn.appendChild(dot);
    btn.appendChild(nameSpan);
    btn.appendChild(countSpan);
    btn.addEventListener('click', () => openOrgDetail(org.name));
    tree.appendChild(btn);
  }
}

// Sidebar toggle
document.getElementById('sidebar-toggle')?.addEventListener('click', () => {
  const sidebar = document.getElementById('sidebar');
  sidebar?.classList.toggle('collapsed');
});

// Sidebar nav item clicks
document.querySelectorAll('.sidebar-item').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.sidebar-item').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    showView(btn.dataset.view);
    state.currentOrgDetail = null;
    renderSidebarOrgTree();
  });
});

function showView(viewName) {
  document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
  const view = document.getElementById(`view-${viewName}`);
  if (view) view.classList.add('active');
}

// ── Quick filter buttons ──────────────────────────────────
document.getElementById('filter-running')?.addEventListener('click', () => {
  state.taskFilter.status = 'running';
  state.taskFilter.org = 'all';
  showView('overview');
  document.querySelectorAll('.sidebar-item').forEach(b => {
    b.classList.toggle('active', b.dataset.view === 'overview');
  });
  renderGlobalTaskTable();
  const sel = document.getElementById('task-status-filter');
  if (sel) sel.value = 'running';
});

document.getElementById('filter-blocked')?.addEventListener('click', () => {
  state.taskFilter.status = 'blocked';
  state.taskFilter.org = 'all';
  showView('overview');
  document.querySelectorAll('.sidebar-item').forEach(b => {
    b.classList.toggle('active', b.dataset.view === 'overview');
  });
  renderGlobalTaskTable();
  const sel = document.getElementById('task-status-filter');
  if (sel) sel.value = 'blocked';
});

document.getElementById('filter-clear')?.addEventListener('click', () => {
  state.taskFilter = { search: '', org: 'all', status: 'all' };
  const si = document.getElementById('task-search-input');
  if (si) si.value = '';
  const os = document.getElementById('task-org-filter');
  if (os) os.value = 'all';
  const ss = document.getElementById('task-status-filter');
  if (ss) ss.value = 'all';
  renderGlobalTaskTable();
});

// ── Render: All Organizations Overview Screen ─────────────
function renderOverview() {
  const f = state.fleet;
  if (!f) return;

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

  document.getElementById('kpi-total-cost').textContent = fmtCurrency(f.token_telemetry?.total_cost_usd);

  updateFleetPacingBadge(f.provider_quotas);
  renderQuotaGauges(f.provider_quotas);
  renderOrganizationsGrid(f.organizations);
  renderTokenTelemetrySection(f.token_telemetry, f.model_spend, f.org_spend);
  renderGlobalTaskTable();
}

function updateFleetPacingBadge(quotas) {
  const pill = document.getElementById('pacing-pill');
  if (!pill || !quotas) return;
  let anyLocked = false, anyOverpaced = false;
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

  for (const key of ['gemini', 'claude', 'openai']) {
    const q = quotas[key];
    if (!q) continue;

    const card = el('div', 'gauge-card');
    const hdr = el('div', 'gauge-card-header');
    hdr.appendChild(el('span', 'gauge-provider-name', q.display_name || key));

    let sCls = 'pill-green', sTxt = '✔ On Track';
    if (q.is_locked || q.projection_status === 'locked_out') { sCls = 'pill-red'; sTxt = '✖ Locked Out'; }
    else if (q.projection_status === 'overpaced') { sCls = 'pill-amber'; sTxt = '⚠ Overpaced'; }
    hdr.appendChild(el('span', `pill ${sCls}`, sTxt));
    card.appendChild(hdr);

    const remainingPct = Math.max(0, Math.min(100, q.five_hour_remaining_pct ?? 100));
    const usedPct = 100 - remainingPct;
    const barOuter = el('div', 'gauge-bar-outer');
    const barInner = el('div', 'gauge-bar-inner');
    barInner.style.width = `${usedPct}%`;
    barInner.className = `gauge-bar-inner ${
      (q.is_locked || usedPct >= 95) ? 'gauge-bar-red' :
      usedPct >= 75 ? 'gauge-bar-amber' : 'gauge-bar-green'
    }`;
    barOuter.appendChild(barInner);
    card.appendChild(barOuter);

    const mRow = el('div', 'gauge-metrics-row');
    mRow.appendChild(el('span', null, `5h Used: ${usedPct.toFixed(1)}% (${remainingPct.toFixed(1)}% Left)`));
    mRow.appendChild(el('span', 'gauge-metric-val', formatCountdown(q.five_hour_resets_at) || 'rolling window'));
    card.appendChild(mRow);

    const bRow = el('div', 'gauge-metrics-row');
    bRow.appendChild(el('span', null, `Burn Rate: ${q.burn_rate_5h ? q.burn_rate_5h.toFixed(2) + '%/turn' : '0.0%/turn'}`));
    bRow.appendChild(el('span', null, `Lockout Limit: ${q.lockout_threshold_pct || 100}%`));
    card.appendChild(bRow);

    const projBox = el('div', 'gauge-projection-box', q.projection_message || 'Sustainable pacing against weekly budget');
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
    card.title = `Click to view ${org.name} details`;

    const top = el('div', 'org-card-title');
    top.appendChild(el('span', 'org-name', org.name));
    top.appendChild(el('span', 'org-prefix', `[${org.issue_prefix || 'ORG'}]`));
    card.appendChild(top);

    const sGrid = el('div', 'org-stats-grid');
    const stats = [
      { n: org.task_counts?.running || 0, l: 'Running' },
      { n: org.active_agents || 0, l: 'Agents' },
      { n: org.task_counts?.blocked || 0, l: 'Blocked' },
    ];
    for (const s of stats) {
      const d = el('div');
      d.appendChild(el('div', 'org-stat-n', String(s.n)));
      d.appendChild(el('div', 'org-stat-l', s.l));
      sGrid.appendChild(d);
    }
    card.appendChild(sGrid);

    const provs = org.active_agents_by_provider || {};
    const provRow = el('div', 'gauge-metrics-row');
    provRow.appendChild(el('span', null, 'Deployments:'));
    provRow.appendChild(el('span', 'gauge-metric-val',
      `Claude: ${provs.claude || 0} · Gemini: ${provs.gemini || 0} · OpenAI: ${provs.openai || 0}`));
    card.appendChild(provRow);

    const spendRow = el('div', 'gauge-metrics-row');
    spendRow.appendChild(el('span', null, `Spend: ${fmtCurrency(org.spent_usd)}`));
    spendRow.appendChild(el('span', null, `Tokens: ${fmtCompactNum(org.spent_tokens)}`));
    card.appendChild(spendRow);

    // Make org card clickable → org detail view
    card.addEventListener('click', () => openOrgDetail(org.name));

    container.appendChild(card);
  }
}

function renderTokenTelemetrySection(telemetry, modelSpend, orgSpend) {
  const aggCol = document.getElementById('telemetry-aggregates');
  const spCol  = document.getElementById('telemetry-spend-breakdown');
  if (!aggCol || !spCol) return;
  aggCol.innerHTML = '';
  spCol.innerHTML = '';

  aggCol.appendChild(el('h3', 'section-title', 'Global Token Consumption'));
  for (const r of [
    { label: 'Total Tokens Ingested', val: fmtNum(telemetry?.total_tokens) },
    { label: 'Prompt Input Tokens',   val: fmtNum(telemetry?.input_tokens) },
    { label: 'Completion Output Tokens', val: fmtNum(telemetry?.output_tokens) },
    { label: 'Cache Read / Reused Tokens', val: fmtNum(telemetry?.cache_read_tokens) },
    { label: 'Total API List-Price Spend', val: fmtCurrency(telemetry?.total_cost_usd) },
  ]) {
    const row = el('div', 'telemetry-item-row');
    row.appendChild(el('span', null, r.label));
    row.appendChild(el('span', 'gauge-metric-val', r.val));
    aggCol.appendChild(row);
  }

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
  if (!tasks.length) tasks = Object.values(state.tasks);

  const sTerm = (state.taskFilter.search || '').toLowerCase().trim();
  const orgFilter = state.taskFilter.org || 'all';
  const statusFilter = state.taskFilter.status || 'all';

  const filtered = tasks.filter(t => {
    if (orgFilter !== 'all' && t.organization !== orgFilter) return false;
    if (statusFilter !== 'all') {
      const st = (t.status || 'active').toLowerCase();
      if (statusFilter === 'running' && st !== 'running' && st !== 'in_progress') return false;
      if (statusFilter === 'active'  && st !== 'active'  && st !== 'todo')        return false;
      if (statusFilter === 'blocked' && st !== 'blocked' && !t.is_blocked)        return false;
      if (statusFilter === 'stopped' && st !== 'stopped' && st !== 'cancelled' && st !== 'paused') return false;
      if (statusFilter === 'errored' && st !== 'errored' && st !== 'error' && st !== 'failed') return false;
      if (statusFilter === 'done'    && st !== 'done')                             return false;
    }
    if (sTerm) {
      const title = (t.title || t.name || '').toLowerCase();
      const id    = (t.identifier || t.id || '').toLowerCase();
      const org   = (t.organization || '').toLowerCase();
      if (!title.includes(sTerm) && !id.includes(sTerm) && !org.includes(sTerm)) return false;
    }
    return true;
  });

  if (!filtered.length) {
    const tr = document.createElement('tr');
    const td = document.createElement('td');
    td.colSpan = 7;
    td.textContent = 'No tasks match current filter criteria.';
    td.style.cssText = 'text-align:center;color:var(--muted);padding:24px;';
    tr.appendChild(td);
    tbody.appendChild(tr);
    return;
  }

  for (const t of filtered) {
    const tr = document.createElement('tr');

    const tdId = el('td', null, t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : '—'));
    tdId.style.cssText = 'font-family:monospace;font-weight:600;';

    const tdTitle  = el('td', null, t.title || t.name || '(untitled)');
    const tdOrg    = el('td', null, t.organization || 'StayPoint');
    const tdStat   = el('td'); tdStat.appendChild(statusPill(t.status));
    const tdPri    = el('td', null, t.priority || 'medium');
    const tdSpend  = el('td', null, `${fmtCurrency(t.spent_usd)} (${fmtCompactNum(t.spent_tokens)} tok)`);
    const tdUp     = el('td', null, fmtTime(t.updated_at || Date.now()));

    for (const td of [tdId, tdTitle, tdOrg, tdStat, tdPri, tdSpend, tdUp]) tr.appendChild(td);
    tr.addEventListener('click', () => openDetail(t.id));
    tbody.appendChild(tr);
  }
}

// ── Org Detail View ────────────────────────────────────────
function openOrgDetail(orgName) {
  const org = (state.fleet?.organizations || []).find(o => o.name === orgName);
  if (!org) return;

  state.currentOrgDetail = orgName;
  showView('org-detail');

  // Mark sidebar nav inactive (no tab selected), update org tree highlight
  document.querySelectorAll('.sidebar-item').forEach(b => b.classList.remove('active'));
  renderSidebarOrgTree();

  renderOrgDetailView(org);
}

function renderOrgDetailView(org) {
  const container = document.getElementById('org-detail-content');
  if (!container) return;
  container.innerHTML = '';

  // Header with back button
  const hdr = el('div', 'org-detail-header');
  const backBtn = el('button', 'back-btn', '← Back');
  backBtn.addEventListener('click', () => {
    state.currentOrgDetail = null;
    showView('overview');
    document.querySelectorAll('.sidebar-item').forEach(b => {
      b.classList.toggle('active', b.dataset.view === 'overview');
    });
    renderSidebarOrgTree();
  });
  hdr.appendChild(backBtn);

  const titleWrap = el('div');
  titleWrap.appendChild(el('div', 'org-detail-name', org.name));
  titleWrap.appendChild(el('div', 'org-detail-prefix', `[${org.issue_prefix || 'ORG'}]`));
  hdr.appendChild(titleWrap);
  container.appendChild(hdr);

  // Stats row
  const statsRow = el('div', 'org-detail-stats');
  const tc = org.task_counts || {};
  const statItems = [
    { val: tc.running || 0,  label: 'Running',  cls: 'highlight-cyan' },
    { val: tc.active  || 0,  label: 'Active' },
    { val: tc.blocked || 0,  label: 'Blocked',  cls: 'highlight-red' },
    { val: tc.done    || 0,  label: 'Done',     cls: 'highlight-green' },
    { val: tc.total   || 0,  label: 'Total' },
    { val: org.active_agents || 0, label: 'Agents', cls: 'highlight-green' },
    { val: fmtCurrency(org.spent_usd), label: 'Spend', isStr: true },
  ];
  for (const s of statItems) {
    const d = el('div', 'org-detail-stat');
    const valEl = el('div', 'org-detail-stat-val' + (s.cls ? ' ' + s.cls : ''), String(s.val));
    d.appendChild(valEl);
    d.appendChild(el('div', 'org-detail-stat-label', s.label));
    statsRow.appendChild(d);
  }
  container.appendChild(statsRow);

  // Active Tasks section
  const orgTasks = (org.tasks || []).concat(
    Object.values(state.tasks).filter(t =>
      t.organization === org.name && !(org.tasks || []).find(ot => ot.id === t.id)
    )
  );

  if (orgTasks.length) {
    const tasksSec = el('div', 'org-detail-section');
    tasksSec.appendChild(el('div', 'org-detail-section-title', `Tasks (${orgTasks.length})`));
    const tbl = document.createElement('table');
    tbl.className = 'global-task-table';
    tbl.style.width = '100%';
    const thead = document.createElement('thead');
    thead.innerHTML = '<tr><th>ID</th><th>Task</th><th>Status</th><th>Priority</th><th>Spend</th></tr>';
    tbl.appendChild(thead);
    const tbody = document.createElement('tbody');
    for (const t of orgTasks) {
      const tr = document.createElement('tr');
      const tdId = el('td', null, t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : '—'));
      tdId.style.cssText = 'font-family:monospace;font-weight:600;';
      const tdTitle = el('td', null, t.title || t.name || '(untitled)');
      const tdStat  = el('td'); tdStat.appendChild(statusPill(t.status));
      const tdPri   = el('td', null, t.priority || '—');
      const tdSpend = el('td', null, fmtCurrency(t.spent_usd));
      for (const td of [tdId, tdTitle, tdStat, tdPri, tdSpend]) tr.appendChild(td);
      tr.addEventListener('click', () => openDetail(t.id));
      tbody.appendChild(tr);
    }
    tbl.appendChild(tbody);
    const wrapper = el('div', 'task-table-wrapper');
    wrapper.appendChild(tbl);
    tasksSec.appendChild(wrapper);
    container.appendChild(tasksSec);
  }

  // Agent Roster section
  const orgAgents = (org.agents || []);
  const agentsSec = el('div', 'org-detail-section');
  agentsSec.appendChild(el('div', 'org-detail-section-title', `Agent Roster (${orgAgents.length || 'by provider'})`));

  if (orgAgents.length) {
    const rosterGrid = el('div', 'agent-roster-grid');
    for (const a of orgAgents) {
      const card = el('div', 'agent-roster-card');
      card.appendChild(el('div', 'agent-roster-name', a.name || a.id?.slice(0, 12)));
      card.appendChild(el('div', 'agent-roster-meta',
        `${a.role || 'agent'} · ${a.provider || 'unknown'} · ${a.status || 'active'}`));
      if (a.last_heartbeat) {
        card.appendChild(el('div', 'muted-text', `Last: ${fmtDateTime(a.last_heartbeat)}`));
      }
      rosterGrid.appendChild(card);
    }
    agentsSec.appendChild(rosterGrid);
  } else {
    // Fall back to provider breakdown
    const provs = org.active_agents_by_provider || {};
    const provBreakdown = el('div', 'agent-roster-grid');
    for (const [provider, count] of Object.entries(provs)) {
      if (!count) continue;
      const card = el('div', 'agent-roster-card');
      card.appendChild(el('div', 'agent-roster-name', `${provider}: ${count} active`));
      card.appendChild(el('div', 'agent-roster-meta', 'Provider breakdown'));
      provBreakdown.appendChild(card);
    }
    if (!Object.values(provs).some(n => n > 0)) {
      provBreakdown.appendChild(el('div', 'panel-field-muted', 'No active agents'));
    }
    agentsSec.appendChild(provBreakdown);
  }
  container.appendChild(agentsSec);

  // Spend breakdown
  const orgSpendData = (state.fleet?.org_spend || []).find(o => o.organization === org.name);
  if (orgSpendData) {
    const spendSec = el('div', 'org-detail-section');
    spendSec.appendChild(el('div', 'org-detail-section-title', 'Spend Breakdown'));
    const spendBox = el('div', 'dashboard-section');
    spendBox.style.padding = '12px 16px';
    for (const row of [
      { label: 'Total Cost (USD)',  val: fmtCurrency(orgSpendData.cost_usd) },
      { label: 'Total Tokens',      val: fmtNum(orgSpendData.total_tokens) },
      { label: 'Input Tokens',      val: fmtNum(orgSpendData.input_tokens) },
      { label: 'Output Tokens',     val: fmtNum(orgSpendData.output_tokens) },
      { label: 'Fleet Share',       val: `${orgSpendData.percentage || 0}%` },
    ]) {
      const r = el('div', 'org-spend-row');
      r.appendChild(el('span', null, row.label));
      r.appendChild(el('span', 'gauge-metric-val', row.val));
      spendBox.appendChild(r);
    }
    spendSec.appendChild(spendBox);
    container.appendChild(spendSec);
  }
}

// ── Render: Kanban ────────────────────────────────────────
const KANBAN_COLS = ['todo', 'in_progress', 'blocked', 'done'];

function renderKanban() {
  const groups = { todo: [], in_progress: [], blocked: [], done: [] };
  for (const t of Object.values(state.tasks)) {
    const col = groups[t.status];
    if (col) col.push(t);
  }
  for (const status of KANBAN_COLS) {
    const list = document.getElementById(`col-${status}`);
    if (!list) continue;
    list.innerHTML = '';
    const tasks = groups[status].sort((a, b) => new Date(b.updated_at || 0) - new Date(a.updated_at || 0));
    for (const t of tasks) list.appendChild(makeTaskCard(t));
  }
}

function makeTaskCard(task) {
  const card = el('div', 'task-card');
  card.dataset.id = task.id;
  card.appendChild(el('div', 'card-title', task.title || task.name || '(untitled)'));
  const meta = el('div', 'card-meta');
  meta.appendChild(el('span', `card-status-dot dot-${task.status}`));
  meta.appendChild(el('span', 'card-id', task.identifier || (task.id ? `#${task.id.slice(0, 8)}` : '')));
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

  // Provider legend
  const legend = document.getElementById('provider-legend');
  if (legend) {
    legend.innerHTML = '';
    const byCounts = {};
    for (const s of sessions) {
      const p = s.agent_type || 'other';
      byCounts[p] = (byCounts[p] || 0) + 1;
    }
    for (const [provider, count] of Object.entries(byCounts)) {
      const badge = el('span', `fleet-provider-badge provider-${provider}`, `${provider}: ${count}`);
      legend.appendChild(badge);
    }
  }

  sessions.sort((a, b) => new Date(b.last_heartbeat_at || 0) - new Date(a.last_heartbeat_at || 0));
  for (const s of sessions) grid.appendChild(makeFleetCard(s));
}

function makeFleetCard(s) {
  const card = el('div', 'fleet-card');

  const topRow = el('div', 'fleet-status');
  topRow.style.justifyContent = 'space-between';
  topRow.style.marginBottom = '6px';

  const agentLine = el('div', 'fleet-agent', s.agent_type || 'agent');
  const provBadge = el('span', `fleet-provider-badge provider-${s.agent_type || 'other'}`,
    s.agent_type || 'other');

  topRow.appendChild(agentLine);
  topRow.appendChild(provBadge);
  card.appendChild(topRow);

  card.appendChild(el('div', 'fleet-repo',
    `${s.repo_path || '—'} (${s.git_branch || '—'})`));

  const statusRow = el('div', 'fleet-status');
  const dotCls = s.status === 'active' ? 'fleet-dot fleet-active'
               : s.status === 'idle'   ? 'fleet-dot fleet-idle'
               : 'fleet-dot fleet-closed';
  statusRow.appendChild(el('span', dotCls));
  statusRow.appendChild(el('span', null, s.status || 'unknown'));
  if (s.last_heartbeat_at) {
    statusRow.appendChild(el('span', 'muted-text', ` · ${fmtTime(s.last_heartbeat_at)}`));
  }
  if (s.hostname && s.hostname !== 'local') {
    statusRow.appendChild(el('span', 'muted-text', ` · ${s.hostname}`));
  }
  card.appendChild(statusRow);
  return card;
}

// ── Render: Boss Card ─────────────────────────────────────
function renderBoss() {
  const container = document.getElementById('boss-container');
  if (!container) return;

  const tasks   = Object.values(state.tasks);
  const todo    = tasks.filter(t => t.status === 'todo').length;
  const inProg  = tasks.filter(t => t.status === 'in_progress').length;
  const blocked = tasks.filter(t => t.status === 'blocked').length;
  const done    = tasks.filter(t => t.status === 'done').length;
  const total   = tasks.length;

  // Prefer fleet aggregator's active_running count (includes Gemini/remote agents);
  // fall back to local session DB count if fleet data is unavailable.
  const sessions = Object.values(state.sessions);
  const active = state.fleet?.global_agents?.active_running
    ?? sessions.filter(s => s.status === 'active').length;

  container.innerHTML = '';

  const card = el('div', 'boss-card');
  const header = el('div', 'boss-header');
  const titleWrap = el('div');
  titleWrap.appendChild(el('div', 'boss-title', 'StayPoint Fleet'));
  titleWrap.appendChild(el('div', 'boss-subtitle',
    `${total} task${total !== 1 ? 's' : ''} · ${active} agent${active !== 1 ? 's' : ''} active`));
  header.appendChild(titleWrap);
  card.appendChild(header);

  const grid = el('div', 'boss-stat-grid');
  for (const { val, label } of [
    { val: inProg,  label: 'In Progress' },
    { val: todo,    label: 'Todo' },
    { val: blocked, label: 'Blocked' },
    { val: done,    label: 'Done' },
    { val: active,  label: 'Agents' },
  ]) {
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
  for (const evt of state.events.slice(0, 50)) {
    const row = el('div', 'event-row');
    row.appendChild(el('span', 'event-ts',   fmtTime(evt.timestamp)));
    row.appendChild(el('span', 'event-type', evt.type || 'event'));
    const data = typeof evt.data === 'object' ? JSON.stringify(evt.data) : String(evt.data ?? '');
    row.appendChild(el('span', 'event-data', data));
    streamEl.appendChild(row);
  }
}

// ── Detail panel ──────────────────────────────────────────
function addPanelField(content, label, value) {
  if (!value && value !== 0) return;
  const field = el('div', 'panel-field');
  field.appendChild(el('div', 'panel-field-label', label));
  if (typeof value === 'string') {
    field.appendChild(el('div', 'panel-field-value', value));
  } else {
    field.appendChild(value);
  }
  content.appendChild(field);
}

function renderDetailContent(content, task) {
  content.innerHTML = '';

  // Title
  const title = task.title || task.name || '(untitled)';
  content.appendChild(el('h2', 'panel-title', title));

  // Meta row: status pill, identifier, priority, org
  const metaRow = el('div', 'panel-meta-row');
  metaRow.appendChild(statusPill(task.status || 'unknown'));
  if (task.identifier) metaRow.appendChild(el('span', 'card-id', task.identifier));
  if (task.priority)   metaRow.appendChild(statusPill(task.priority));
  if (task.organization) metaRow.appendChild(el('span', 'pill', task.organization));
  content.appendChild(metaRow);

  // Description (markdown)
  const desc = task.description || task.block_reason || '';
  if (desc) {
    const descField = el('div', 'panel-field');
    descField.appendChild(el('div', 'panel-field-label', 'Description'));
    descField.appendChild(mdEl(desc));
    content.appendChild(descField);
  }

  // Properties grid
  addPanelField(content, 'Stage',    task.execution_stage || task.status);
  addPanelField(content, 'Assignee', task.assignee_name || task.checkout_agent_id || null);
  addPanelField(content, 'Project',  task.project || null);
  addPanelField(content, 'Repo',     task.repo_path ? `${task.repo_path} (${task.git_branch || 'main'})` : null);

  // Spend
  if (task.spent_usd || task.spent_tokens) {
    addPanelField(content, 'Spend',
      `${fmtCurrency(task.spent_usd)} · ${fmtCompactNum(task.spent_tokens)} tokens`);
  }

  // Budget
  if (task.max_budget_usd) {
    addPanelField(content, 'Budget', `${fmtCurrency(task.max_budget_usd)} · ${task.max_turns || 50} turns max`);
  }

  // Blocker
  if (task.is_blocked) {
    const blockerWrap = el('div', 'panel-field');
    blockerWrap.appendChild(el('div', 'panel-field-label', 'Blocked'));
    const tag = el('span', 'panel-blocker-tag', `⚠ ${task.block_reason || 'blocked'}`);
    blockerWrap.appendChild(tag);
    content.appendChild(blockerWrap);
  }

  // Linked issues (parent)
  if (task.parent_id) {
    addPanelField(content, 'Parent Task', `#${task.parent_id.slice(0, 12)}`);
  }

  // Timestamps
  if (task.created_at || task.updated_at) {
    const tsField = el('div', 'panel-field');
    tsField.appendChild(el('div', 'panel-field-label', 'Timestamps'));
    const tsVal = el('div', 'panel-field-muted');
    if (task.created_at) tsVal.textContent = `Created: ${fmtDateTime(task.created_at)}`;
    if (task.updated_at) tsVal.textContent += `  ·  Updated: ${fmtDateTime(task.updated_at)}`;
    tsField.appendChild(tsVal);
    content.appendChild(tsField);
  }

  // Raw ID
  const idField = el('div', 'panel-field');
  idField.appendChild(el('div', 'panel-field-label', 'Internal ID'));
  idField.appendChild(el('div', 'panel-field-muted', task.id || '—'));
  content.appendChild(idField);
}

// Fleet tasks have UUID format (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
// Local tasks use the "task-XXXXXXXX" prefix format.
function isFleetTaskId(id) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id);
}

async function openDetail(taskId) {
  const panel   = document.getElementById('detail-panel');
  const content = document.getElementById('panel-content');

  panel.classList.remove('hidden');
  content.innerHTML = '<p style="color:var(--muted)">Loading…</p>';

  const apiBase = isFleetTaskId(taskId) ? '/api/fleet/tasks' : '/api/tasks';

  try {
    const resp = await apiFetch(`${apiBase}/${taskId}`);
    // Local handler returns { task: {...}, comments: [...] }
    // Fleet proxy returns the Paperclip issue object directly
    const task = resp.task || resp;
    const inlineComments = resp.comments || [];

    renderDetailContent(content, task);

    if (inlineComments.length) {
      appendComments(content, inlineComments);
    } else {
      // Try loading comments separately (fleet tasks)
      try {
        const commResp = await apiFetch(`${apiBase}/${taskId}/comments`);
        const comments = commResp.comments || (Array.isArray(commResp) ? commResp : []);
        if (comments.length) appendComments(content, comments);
      } catch { /* comments optional */ }
    }

  } catch {
    // Fall back to cached fleet task data if API fails
    const cached = state.tasks[taskId];
    if (cached) {
      renderDetailContent(content, cached);
    } else {
      const p = el('p', null, 'Task not found or failed to load.');
      p.style.color = 'var(--red)';
      content.innerHTML = '';
      content.appendChild(p);
    }
  }
}

function appendComments(container, comments) {
  container.appendChild(el('div', 'panel-section-title', `Comments (${comments.length})`));
  for (const c of comments) {
    const row = el('div', 'panel-comment');
    const authorType = c.authorType || c.author_type || '';
    const author = authorType === 'agent' ? 'agent' : (c.author || 'user');
    const ts = c.createdAt || c.created_at || c.timestamp || '';
    if (author || ts) {
      row.appendChild(el('div', 'panel-comment-meta', `${author} · ${ts ? fmtDateTime(ts) : ''}`));
    }
    row.appendChild(mdEl(c.body || c.message || ''));
    container.appendChild(row);
  }
}

document.getElementById('panel-close').addEventListener('click', () => {
  document.getElementById('detail-panel').classList.add('hidden');
});

// ── Render All ────────────────────────────────────────────
function renderAll() {
  renderOverview();
  renderKanban();
  renderFleet();
  renderBoss();

  // Re-render org detail if one is open and fleet data refreshed
  if (state.currentOrgDetail) {
    const org = (state.fleet?.organizations || []).find(o => o.name === state.currentOrgDetail);
    if (org) renderOrgDetailView(org);
  }

  renderSidebarOrgTree();
}

// ── Filter listeners ──────────────────────────────────────
document.getElementById('task-search-input')?.addEventListener('input', (e) => {
  state.taskFilter.search = e.target.value;
  renderGlobalTaskTable();
});

document.getElementById('task-org-filter')?.addEventListener('change', (e) => {
  state.taskFilter.org = e.target.value;
  renderGlobalTaskTable();
});

document.getElementById('task-status-filter')?.addEventListener('change', (e) => {
  state.taskFilter.status = e.target.value;
  renderGlobalTaskTable();
});

// ── Boot ──────────────────────────────────────────────────
loadAll().then(() => connectSSE());
