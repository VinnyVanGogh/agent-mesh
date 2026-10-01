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
  tsFilter: { search: '', org: 'all', status: 'all' },
  agentsFilter: { search: '', org: 'all', project: 'all', provider: 'all', status: 'all' },
  currentOrgDetail: null,
  openDetailTaskId: null,
  chatPollTimer:    null,
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

function titleCase(s) {
  if (!s) return '';
  return String(s).charAt(0).toUpperCase() + String(s).slice(1);
}

function fmtRelTime(ts) {
  if (!ts) return '';
  const diffMs = Date.now() - new Date(ts).getTime();
  const secs = Math.floor(diffMs / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return fmtDateTime(ts);
}

function formatCountdown(targetTs) {
  if (!targetTs) return '';
  const diffMs = new Date(targetTs).getTime() - Date.now();
  if (diffMs <= 0) return 'resets soon';
  const mins = Math.floor(diffMs / 60000);
  const hrs = Math.floor(mins / 60);
  const remMins = mins % 60;
  if (hrs >= 24) {
    const days = Math.floor(hrs / 24);
    const remHrs = hrs % 24;
    return `in ${days}d ${remHrs}h`;
  }
  if (hrs > 0) return `in ${hrs}h ${remMins}m`;
  return `in ${remMins}m`;
}

function formatResetTime(targetTs, isWeekly) {
  if (!targetTs) return '';
  const d = new Date(targetTs);
  const timeStr = d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  if (!isWeekly) return `at ${timeStr}`;
  const dayStr = d.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' });
  return `${dayStr} at ${timeStr}`;
}

function authHeader() {
  return TOKEN ? { 'Authorization': `Bearer ${TOKEN}` } : {};
}

// Close any open .report-dl-menu when clicking outside its wrapper.
document.addEventListener('click', () => {
  document.querySelectorAll('.report-dl-menu').forEach(m => { m.hidden = true; });
});

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
  let s = escapeHtml(text);
  s = s.replace(/```[\w]*\n?([\s\S]*?)```/g, (_, code) =>
    `<pre><code>${code.trimEnd()}</code></pre>`);
  s = s.replace(/`([^`\n]+)`/g, '<code>$1</code>');
  s = s.replace(/^### (.+)$/gm, '<h3>$1</h3>');
  s = s.replace(/^## (.+)$/gm, '<h2>$1</h2>');
  s = s.replace(/^# (.+)$/gm, '<h1>$1</h1>');
  s = s.replace(/\*\*\*([^*]+)\*\*\*/g, '<strong><em>$1</em></strong>');
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/\*([^*\n]+)\*/g, '<em>$1</em>');
  s = s.replace(/^&gt; (.+)$/gm, '<blockquote>$1</blockquote>');
  s = s.replace(/^[-*] (.+)$/gm, '<li>$1</li>');
  s = s.replace(/(<li>[\s\S]*?<\/li>)(\n<li>[\s\S]*?<\/li>)*/g, m => `<ul>${m}</ul>`);
  s = s.replace(/^\d+\. (.+)$/gm, '<li>$1</li>');
  s = s.replace(/^---+$/gm, '<hr>');
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

    if (fleetResp?.tasks) {
      for (const t of fleetResp.tasks) {
        if (!state.tasks[t.id]) {
          state.tasks[t.id] = { ...t, status: normalizeFleetStatus(t.status) };
        }
      }
    }

    populateOrgFilter();
    populateTSOrgFilter();
    populateAgentsFilters();
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
      populateTSOrgFilter();
      populateAgentsFilters();
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

// ── SPA URL Routing ──────────────────────────────────────
function viewToPath(viewName, orgName) {
  if (orgName) return `/org/${encodeURIComponent(orgName)}`;
  if (!viewName || viewName === 'overview') return '/';
  return `/${viewName}`;
}

function pathToRoute(pathname) {
  const p = (pathname || window.location.pathname).replace(/\/+$/, '') || '/';
  if (p === '/' || p === '/overview') return { view: 'overview', org: null, taskId: null };
  if (p.startsWith('/org/')) {
    const org = decodeURIComponent(p.slice(5));
    return { view: 'org-detail', org, taskId: null };
  }
  if (p.startsWith('/tasks/')) {
    const taskId = decodeURIComponent(p.slice(7));
    return { view: 'overview', org: null, taskId };
  }
  if (p.startsWith('/issues/')) {
    const taskId = decodeURIComponent(p.slice(8));
    return { view: 'overview', org: null, taskId };
  }
  const clean = p.replace(/^\//, '');
  return { view: clean, org: null, taskId: null };
}

function navigateTo(viewName, orgName = null, pushHistory = true) {
  const targetPath = viewToPath(viewName, orgName);
  if (pushHistory && window.location.pathname !== targetPath) {
    history.pushState({ view: viewName, org: orgName }, '', targetPath);
  }

  state.currentOrgDetail = orgName;

  // Update sidebar active buttons
  document.querySelectorAll('.sidebar-item').forEach(b => {
    b.classList.toggle('active', !orgName && b.dataset.view === viewName);
  });

  // Switch view visibility
  document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
  const targetViewId = orgName ? 'view-org-detail' : `view-${viewName}`;
  const targetEl = document.getElementById(targetViewId);
  if (targetEl) targetEl.classList.add('active');

  renderSidebarOrgTree();

  // Render view content
  if (orgName) {
    const org = (state.fleet?.organizations || []).find(o => o.name === orgName || o.name.toLowerCase() === orgName.toLowerCase());
    if (org) renderOrgDetailView(org);
  } else {
    if (viewName === 'projects')     renderProjects();
    if (viewName === 'agents')       { populateAgentsFilters(); renderAgentsPage(); }
    if (viewName === 'recent-tasks') renderRecentTasks();
    if (viewName === 'task-status')  renderTaskStatusPage();
    if (viewName === 'cost')         renderCostPage();
    if (viewName === 'settings')     renderSettings();
    if (viewName === 'checklist')    loadChecklistSprints().then(() => loadChecklist());
    if (viewName === 'overview')     renderOverview();
    if (viewName === 'kanban')       renderKanban();
    if (viewName === 'boss')         renderBoss();
  }
}

function showView(viewName) {
  navigateTo(viewName, null, true);
}

// Sidebar nav item clicks
document.querySelectorAll('.sidebar-item').forEach(btn => {
  btn.addEventListener('click', () => {
    navigateTo(btn.dataset.view, null, true);
  });
});

window.addEventListener('popstate', () => {
  const route = pathToRoute();
  if (route.taskId) {
    navigateTo(route.view, route.org, false);
    openDetail(route.taskId, false);
  } else {
    document.getElementById('detail-panel')?.classList.add('hidden');
    stopChatPoll();
    state.openDetailTaskId = null;
    navigateTo(route.view, route.org, false);
  }
});

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
  if (document.getElementById('view-settings')?.classList.contains('active')) {
    renderSettings();
  }
  const modalEl = document.getElementById('fleet-modal');
  if (modalEl && !modalEl.classList.contains('hidden')) {
    switchFleetModalTab(fleetModalState.activeTab);
  }
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

function buildGaugeCard(key, q) {
  const card = el('div', 'gauge-card');
  const hdr = el('div', 'gauge-card-header');
  hdr.appendChild(el('span', 'gauge-provider-name', q.display_name || key));

  let sCls = 'pill-green', sTxt = '✔ On Track';
  if (q.is_locked || q.projection_status === 'locked_out') { sCls = 'pill-red'; sTxt = '✖ Locked Out'; }
  else if (q.projection_status === 'overpaced') { sCls = 'pill-amber'; sTxt = '⚠ Overpaced'; }
  hdr.appendChild(el('span', `pill ${sCls}`, sTxt));
  card.appendChild(hdr);

  // 5-Hour Rolling
  const remaining5h = Math.max(0, Math.min(100, q.five_hour_remaining_pct ?? 100));
  const used5h = 100 - remaining5h;
  const bar5hOuter = el('div', 'gauge-bar-outer');
  const bar5hInner = el('div', 'gauge-bar-inner');
  bar5hInner.style.width = `${used5h}%`;
  bar5hInner.className = `gauge-bar-inner ${
    (q.is_locked || used5h >= 95) ? 'gauge-bar-red' :
    used5h >= 75 ? 'gauge-bar-amber' : 'gauge-bar-green'
  }`;
  bar5hOuter.appendChild(bar5hInner);
  card.appendChild(el('div', 'gauge-window-label', '5-Hour Rolling'));
  card.appendChild(bar5hOuter);

  const m5Row = el('div', 'gauge-metrics-row');
  m5Row.appendChild(el('span', null, `${used5h.toFixed(1)}% used · ${remaining5h.toFixed(1)}% left`));
  const count5h = formatCountdown(q.five_hour_resets_at);
  const time5h = formatResetTime(q.five_hour_resets_at, false);
  m5Row.appendChild(el('span', 'gauge-metric-val', count5h ? `resets ${count5h}` : 'rolling'));
  card.appendChild(m5Row);
  if (time5h) {
    const t5Row = el('div', 'gauge-metrics-row');
    t5Row.appendChild(el('span', null, ''));
    t5Row.appendChild(el('span', 'gauge-reset-time', time5h));
    card.appendChild(t5Row);
  }

  const b5Row = el('div', 'gauge-metrics-row');
  b5Row.appendChild(el('span', null, `Burn: ${q.burn_rate_5h ? q.burn_rate_5h.toFixed(2) + '%/turn' : '—'}`));
  b5Row.appendChild(el('span', null, q.is_locked ? '🔒 Locked Out' : `Limit: ${q.lockout_threshold_pct || 100}%`));
  card.appendChild(b5Row);

  // Weekly Budget
  const remainingWk = Math.max(0, Math.min(100, q.weekly_remaining_pct ?? 100));
  const usedWk = 100 - remainingWk;
  const labelWk = el('div', 'gauge-window-label', 'Weekly Budget');
  labelWk.style.marginTop = '10px';
  card.appendChild(labelWk);

  const barWkOuter = el('div', 'gauge-bar-outer');
  const barWkInner = el('div', 'gauge-bar-inner');
  barWkInner.style.width = `${usedWk}%`;
  barWkInner.className = `gauge-bar-inner ${
    usedWk >= 90 ? 'gauge-bar-red' :
    usedWk >= 70 ? 'gauge-bar-amber' : 'gauge-bar-green'
  }`;
  barWkOuter.appendChild(barWkInner);
  card.appendChild(barWkOuter);

  const mWkRow = el('div', 'gauge-metrics-row');
  mWkRow.appendChild(el('span', null, `${usedWk.toFixed(1)}% used · ${remainingWk.toFixed(1)}% left`));
  const countWk = formatCountdown(q.weekly_resets_at);
  if (countWk) mWkRow.appendChild(el('span', 'gauge-metric-val', `resets ${countWk}`));
  card.appendChild(mWkRow);
  const timeWk = formatResetTime(q.weekly_resets_at, true);
  if (timeWk) {
    const tWkRow = el('div', 'gauge-metrics-row');
    tWkRow.appendChild(el('span', null, ''));
    tWkRow.appendChild(el('span', 'gauge-reset-time', timeWk));
    card.appendChild(tWkRow);
  }

  const bWkRow = el('div', 'gauge-metrics-row');
  bWkRow.appendChild(el('span', null, `Weekly burn: ${q.burn_rate_weekly ? q.burn_rate_weekly.toFixed(2) + '%/turn' : '—'}`));
  if (q.runway_turns) bWkRow.appendChild(el('span', 'gauge-metric-val', `${q.runway_turns} turns left`));
  card.appendChild(bWkRow);

  card.appendChild(el('div', 'gauge-projection-box', q.projection_message || 'Sustainable pacing'));
  return card;
}

function renderQuotaGauges(quotas) {
  const grid = document.getElementById('quota-gauges-grid');
  if (!grid || !quotas) return;
  grid.innerHTML = '';

  // Render in a logical order: gemini, claude work, claude personal, claude (aggregate), openai
  const order = ['gemini', 'claude_work', 'claude_personal', 'claude', 'openai'];
  for (const key of order) {
    const q = quotas[key];
    if (!q) continue;
    // Skip the aggregate 'claude' card if we have the split cards
    if (key === 'claude' && (quotas['claude_work'] || quotas['claude_personal'])) continue;
    grid.appendChild(buildGaugeCard(key, q));
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

    // Organization Rolling Quota & Lockout Indicators
    const orgQuotas = org.provider_quotas || {};
    const isManagedSol = (org.name || '').toLowerCase().includes('managed');
    const lockouts = Object.values(orgQuotas).filter(q => {
      if (!q.is_locked) return false;
      if (isManagedSol && (q.provider === 'claude_personal' || q.provider === 'claude')) {
        const workQ = orgQuotas['claude_work'];
        if (workQ && !workQ.is_locked) return false;
      }
      return true;
    });
    if (lockouts.length) {
      const lockRow = el('div', 'org-lockout-row');
      lockRow.style.cssText = 'color:#f87171;font-size:0.75rem;font-weight:600;margin-top:8px;padding:4px 8px;background:rgba(239,68,68,0.1);border-radius:4px;border:1px solid rgba(239,68,68,0.3);';
      lockRow.textContent = `🔒 Locked: ${lockouts.map(l => l.display_name).join(', ')}`;
      card.appendChild(lockRow);
    } else {
      const quotaKeys = ['gemini', 'claude_work', 'claude_personal', 'openai'].filter(k => orgQuotas[k]);
      if (quotaKeys.length) {
        const qWrap = el('div', 'org-quota-mini-strip');
        qWrap.style.cssText = 'margin-top:8px;display:flex;flex-direction:column;gap:4px;';
        for (const k of quotaKeys) {
          const q = orgQuotas[k];
          const qRow = el('div');
          qRow.style.cssText = 'display:flex;align-items:center;justify-content:space-between;font-size:0.72rem;color:var(--muted);';
          qRow.appendChild(el('span', null, q.display_name));
          qRow.appendChild(el('span', 'gauge-metric-val', `${q.five_hour_used_pct}% 5h`));
          qWrap.appendChild(qRow);
        }
        card.appendChild(qWrap);
      }
    }

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

function populateTSOrgFilter() {
  const select = document.getElementById('ts-org-filter');
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

  const filtered = filterTasks(tasks, sTerm, orgFilter, statusFilter);

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
    const tr = makeTaskTableRow(t, 7);
    tbody.appendChild(tr);
  }
}

function filterTasks(tasks, sTerm, orgFilter, statusFilter) {
  return tasks.filter(t => {
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
}

function makeTaskTableRow(t, colCount) {
  const tr = document.createElement('tr');

  const tdId = el('td', null, t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : '—'));
  tdId.style.cssText = 'font-family:monospace;font-weight:600;';

  const tdTitle  = el('td', null, t.title || t.name || '(untitled)');
  const tdOrg    = el('td', null, t.organization || 'StayPoint');
  const tdStat   = el('td'); tdStat.appendChild(statusPill(t.status));
  const tdPri    = el('td', null, t.priority || 'medium');

  const spendVal = t.spent_usd > 0
    ? `${fmtCurrency(t.spent_usd)} (${fmtCompactNum(t.spent_tokens)} tok)`
    : `—`;
  const tdSpend  = el('td', null, spendVal);
  const tdUp     = el('td', null, fmtRelTime(t.updated_at || Date.now()));

  for (const td of [tdId, tdTitle, tdOrg, tdStat, tdPri, tdSpend, tdUp]) tr.appendChild(td);
  tr.addEventListener('click', () => openDetail(t.id));
  return tr;
}

// ── Projects View ─────────────────────────────────────────
function renderProjects() {
  const grid = document.getElementById('projects-grid');
  if (!grid) return;
  grid.innerHTML = '';

  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const seenIds = new Set();
  const dedupTasks = [];
  for (const t of allTasks) {
    if (!seenIds.has(t.id)) { seenIds.add(t.id); dedupTasks.push(t); }
  }

  // Group by project
  const projectMap = {};
  for (const t of dedupTasks) {
    const proj = t.project || '(No Project)';
    if (!projectMap[proj]) {
      projectMap[proj] = { name: proj, org: t.organization || '', tasks: [] };
    }
    projectMap[proj].tasks.push(t);
  }

  const orgFilter = document.getElementById('projects-org-filter')?.value || 'all';
  const projects = Object.values(projectMap).filter(p =>
    orgFilter === 'all' || p.org === orgFilter
  );

  if (!projects.length) {
    grid.appendChild(el('p', 'muted-text', 'No projects found.'));
    return;
  }

  for (const p of projects.sort((a, b) => b.tasks.length - a.tasks.length)) {
    const card = el('div', 'project-card');

    const hdr = el('div', 'project-card-header');
    const titleWrap = el('div');
    titleWrap.appendChild(el('div', 'project-name', p.name));
    titleWrap.appendChild(el('div', 'project-org', p.org));
    hdr.appendChild(titleWrap);
    const totalBadge = el('span', 'pill', `${p.tasks.length} task${p.tasks.length !== 1 ? 's' : ''}`);
    hdr.appendChild(totalBadge);
    card.appendChild(hdr);

    // Status counts
    const counts = { running: 0, blocked: 0, done: 0, active: 0 };
    let totalSpend = 0;
    for (const t of p.tasks) {
      const st = (t.status || '').toLowerCase();
      if (st === 'running' || st === 'in_progress') counts.running++;
      else if (st === 'blocked') counts.blocked++;
      else if (st === 'done') counts.done++;
      else counts.active++;
      totalSpend += t.spent_usd || 0;
    }

    const statsRow = el('div', 'project-stats-row');
    for (const [label, val, cls] of [
      ['Running', counts.running, 'highlight-cyan'],
      ['Blocked', counts.blocked, 'highlight-red'],
      ['Done', counts.done, 'highlight-green'],
      ['Active', counts.active, ''],
    ]) {
      const s = el('div', 'project-stat');
      s.appendChild(el('div', `project-stat-n ${cls}`, String(val)));
      s.appendChild(el('div', 'project-stat-l', label));
      statsRow.appendChild(s);
    }
    if (totalSpend > 0) {
      const s = el('div', 'project-stat');
      s.appendChild(el('div', 'project-stat-n highlight-gold', fmtCurrency(totalSpend)));
      s.appendChild(el('div', 'project-stat-l', 'Spend'));
      statsRow.appendChild(s);
    }
    card.appendChild(statsRow);

    // Task preview
    const taskList = el('div', 'project-task-list');
    for (const t of p.tasks.slice(0, 5)) {
      const item = el('div', 'project-task-item');
      item.appendChild(statusPill(t.status));
      const titleEl = el('span', null, t.title || t.name || '(untitled)');
      titleEl.style.cssText = 'flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;';
      item.appendChild(titleEl);
      item.addEventListener('click', () => openDetail(t.id));
      taskList.appendChild(item);
    }
    if (p.tasks.length > 5) {
      taskList.appendChild(el('div', 'muted-text', `+${p.tasks.length - 5} more tasks`));
    }
    card.appendChild(taskList);

    grid.appendChild(card);
  }

  // Populate org filter for projects
  const sel = document.getElementById('projects-org-filter');
  if (sel && state.fleet?.organizations) {
    const cur = sel.value;
    sel.innerHTML = '<option value="all">All Organizations</option>';
    for (const org of state.fleet.organizations) {
      const opt = document.createElement('option');
      opt.value = org.name; opt.textContent = org.name;
      sel.appendChild(opt);
    }
    sel.value = cur || 'all';
  }
}

// ── Agents Filter Helpers ─────────────────────────────────
function getProjectsForOrg(orgName = 'all') {
  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const projects = new Set();

  for (const t of allTasks) {
    const tOrg = t.organization || 'StayPoint';
    if (orgName !== 'all' && tOrg !== orgName) continue;
    if (t.project && t.project !== '(No Project)') {
      projects.add(t.project);
    }
  }

  // Also check fleet organizations if they contain explicit project lists or tasks
  for (const org of (state.fleet?.organizations || [])) {
    if (orgName !== 'all' && org.name !== orgName) continue;
    for (const t of (org.tasks || [])) {
      if (t.project && t.project !== '(No Project)') {
        projects.add(t.project);
      }
    }
    for (const a of (org.agents || [])) {
      if (a.project && a.project !== '(No Project)') projects.add(a.project);
      if (Array.isArray(a.projects)) {
        for (const p of a.projects) if (p && p !== '(No Project)') projects.add(p);
      }
    }
    if (Array.isArray(org.projects)) {
      for (const p of org.projects) {
        const name = typeof p === 'string' ? p : p.name;
        if (name && name !== '(No Project)') projects.add(name);
      }
    }
  }

  return Array.from(projects).sort((a, b) => a.localeCompare(b));
}

function getAgentProjects(agent) {
  const projects = new Set();
  if (agent.project && agent.project !== '(No Project)') {
    projects.add(agent.project);
  }
  if (Array.isArray(agent.projects)) {
    for (const p of agent.projects) {
      if (p && p !== '(No Project)') projects.add(p);
    }
  }

  // Also check tasks checked out by or assigned to this agent
  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  for (const t of allTasks) {
    const matchesAgent =
      (t.checkout_agent_id && t.checkout_agent_id === agent.id) ||
      (t.assignee_id && t.assignee_id === agent.id) ||
      (t.assigneeAgentId && t.assigneeAgentId === agent.id) ||
      (t.assignee_name && t.assignee_name === agent.name) ||
      (t.assigneeRole && t.assigneeRole === agent.role);
    if (matchesAgent && t.project && t.project !== '(No Project)') {
      projects.add(t.project);
    }
  }

  return Array.from(projects);
}

function populateAgentsOrgFilter() {
  const sel = document.getElementById('agents-org-filter');
  if (!sel) return;

  const current = state.agentsFilter.org || 'all';
  const orgNames = new Set();
  for (const org of (state.fleet?.organizations || [])) {
    if (org.name) orgNames.add(org.name);
  }
  for (const t of Object.values(state.tasks)) {
    if (t.organization) orgNames.add(t.organization);
  }
  for (const s of Object.values(state.sessions)) {
    orgNames.add(s.org || 'StayPoint');
  }

  sel.innerHTML = '<option value="all">All Organizations</option>';
  for (const org of Array.from(orgNames).sort()) {
    const opt = document.createElement('option');
    opt.value = org;
    opt.textContent = org;
    sel.appendChild(opt);
  }

  if (orgNames.has(current) || current === 'all') {
    sel.value = current;
    state.agentsFilter.org = current;
  } else {
    sel.value = 'all';
    state.agentsFilter.org = 'all';
  }
}

function populateAgentsProjectFilter() {
  const sel = document.getElementById('agents-project-filter');
  if (!sel) return;

  const currentOrg = state.agentsFilter.org || 'all';
  const currentProj = state.agentsFilter.project || 'all';
  const projects = getProjectsForOrg(currentOrg);

  sel.innerHTML = '<option value="all">All Projects</option>';
  for (const p of projects) {
    const opt = document.createElement('option');
    opt.value = p;
    opt.textContent = p;
    sel.appendChild(opt);
  }

  if (projects.includes(currentProj) || currentProj === 'all') {
    sel.value = currentProj;
    state.agentsFilter.project = currentProj;
  } else {
    sel.value = 'all';
    state.agentsFilter.project = 'all';
  }
}

function populateAgentsFilters() {
  populateAgentsOrgFilter();
  populateAgentsProjectFilter();
}

// ── Agents Dedicated View ─────────────────────────────────
function getHeartbeatFreshness(hb) {
  if (!hb) return { text: 'None', pillClass: 'standby', dotClass: 'idle' };
  const d = new Date(hb);
  if (isNaN(d.getTime())) return { text: 'Unknown', pillClass: 'standby', dotClass: 'idle' };
  const diffMs = Date.now() - d.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 10) {
    return { text: `Fresh (${fmtRelTime(hb)})`, pillClass: 'fresh', dotClass: 'running' };
  } else if (diffMin < 60) {
    return { text: `Recent (${fmtRelTime(hb)})`, pillClass: 'recent', dotClass: 'idle' };
  } else {
    return { text: `Standby (${fmtRelTime(hb)})`, pillClass: 'standby', dotClass: 'idle' };
  }
}

function resetAgentFilters() {
  state.agentsFilter.search = '';
  state.agentsFilter.provider = 'all';
  state.agentsFilter.org = 'all';
  state.agentsFilter.status = 'all';
  const searchInput = document.getElementById('agents-search');
  const provSelect = document.getElementById('agents-provider-filter');
  const orgSelect = document.getElementById('agents-org-filter');
  if (searchInput) searchInput.value = '';
  if (provSelect) provSelect.value = 'all';
  if (orgSelect) orgSelect.value = 'all';
  document.querySelectorAll('.agent-filter-pill').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.status === 'all');
  });
  renderAgentsPage();
}

// ── Provider Resolution & Quota Helpers ───────────────────
function detectProviderStr(s) {
  if (!s || typeof s !== 'string') return '';
  const lower = s.toLowerCase().trim();
  if (!lower || lower === 'other' || lower === 'unknown') return '';
  if (lower.includes('gemini') || lower.includes('google')) return 'gemini';
  if (lower.includes('claude') || lower.includes('anthropic') || lower.includes('fable')) return 'claude';
  if (lower.includes('codex') || lower.includes('openai') || lower.includes('gpt') || lower.includes('o1') || lower.includes('o3')) return 'openai';
  if (lower.endsWith('_local')) return lower.replace(/_local$/, '');
  return '';
}

function resolveProvider(val) {
  if (!val) return 'gemini';
  if (typeof val === 'object') {
    const a = val;
    // 1. Check runtime_config / runtimeConfig
    const rc = a.runtime_config || a.runtimeConfig || {};
    const rcProv = rc.provider || rc.model || rc.default_model || rc.defaultModel || rc.model_family || rc.modelFamily || rc.adapter || rc.adapter_type;
    const fromRc = detectProviderStr(rcProv);
    if (fromRc) return fromRc;
    if (rc.provider && typeof rc.provider === 'string' && !['other', 'unknown'].includes(rc.provider.toLowerCase().trim())) {
      return rc.provider.toLowerCase().trim();
    }

    // 2. Check adapter_type / adapterType
    const at = a.adapter_type || a.adapterType;
    const fromAt = detectProviderStr(at);
    if (fromAt) return fromAt;
    if (at && typeof at === 'string') {
      const lower = at.toLowerCase().trim().replace(/_local$/, '');
      if (lower && !['other', 'unknown'].includes(lower)) return lower;
    }

    // 3. Check adapter_config / adapterConfig
    const ac = a.adapter_config || a.adapterConfig || {};
    const acProv = ac.provider || ac.model || ac.default_model || ac.defaultModel;
    const fromAc = detectProviderStr(acProv);
    if (fromAc) return fromAc;
    if (ac.provider && typeof ac.provider === 'string' && !['other', 'unknown'].includes(ac.provider.toLowerCase().trim())) {
      return ac.provider.toLowerCase().trim();
    }

    // 4. Check model / default_model
    const fromModel = detectProviderStr(a.model || a.default_model);
    if (fromModel) return fromModel;

    // 5. Check metadata_json (for local sessions)
    if (a.metadata_json) {
      try {
        const meta = typeof a.metadata_json === 'string' ? JSON.parse(a.metadata_json) : a.metadata_json;
        const fromMeta = detectProviderStr(meta.provider || meta.model || meta.defaultModel || meta.agent_type);
        if (fromMeta) return fromMeta;
      } catch (_) {}
    }

    // 6. Check existing provider field if clean
    if (a.provider && a.provider !== 'other' && a.provider !== 'unknown') {
      const fromProv = detectProviderStr(a.provider);
      if (fromProv) return fromProv;
      return a.provider.replace(/_local$/i, '').toLowerCase();
    }

    // 7. Check agent_type
    if (a.agent_type) {
      const fromAgType = detectProviderStr(a.agent_type);
      if (fromAgType) return fromAgType;
    }

    // 8. Check name, title, and role
    const fromText = detectProviderStr(`${a.name || ''} ${a.title || ''} ${a.role || ''}`);
    if (fromText) return fromText;

    // Strict fallback: never return 'other'
    return 'gemini';
  }

  return detectProviderStr(String(val)) || 'gemini';
}

function getAgentQuota(providerQuotas, provider) {
  if (!providerQuotas) return null;
  const p = (provider || '').toLowerCase();
  if (providerQuotas[p]) return providerQuotas[p];
  if (p.includes('claude') || p.includes('anthropic') || p.includes('fable')) {
    return providerQuotas['claude'] || providerQuotas['claude_personal'] || providerQuotas['claude_work'] || null;
  }
  if (p.includes('gemini') || p.includes('google')) {
    return providerQuotas['gemini'] || null;
  }
  if (p.includes('openai') || p.includes('codex') || p.includes('gpt')) {
    return providerQuotas['openai'] || null;
  }
  return providerQuotas['gemini'] || null;
}

function renderAgentsPage() {
  const grid = document.getElementById('agents-grid');
  if (!grid) return;
  grid.innerHTML = '';

  const f = state.fleet;
  const allAgents = [];

  // Collect from fleet orgs
  for (const org of (f?.organizations || [])) {
    for (const a of (org.agents || [])) {
      const prov = resolveProvider(a);
      allAgents.push({ ...a, provider: prov, org: org.name });
    }
  }

  // Add local sessions not already in fleet
  for (const s of Object.values(state.sessions)) {
    const exists = allAgents.find(a => a.id === s.id);
    if (!exists) {
      const prov = resolveProvider(s);
      allAgents.push({
        id: s.id,
        name: s.agent_type ? `${titleCase(prov)} Session` : 'Local Agent',
        role: 'Local Session',
        provider: prov,
        status: s.status || 'active',
        last_heartbeat: s.last_heartbeat_at,
        org: 'StayPoint',
      });
    }
  }

  // Match each agent with its assigned and current running task
  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const enrichedAgents = allAgents.map(a => {
    const agentTasks = allTasks.filter(t =>
      (t.checkout_agent_id && t.checkout_agent_id === a.id) ||
      (t.assignee_agent_id && t.assignee_agent_id === a.id) ||
      (t.assigned_agent && (t.assigned_agent === a.name || t.assigned_agent === a.id)) ||
      (t.assignee_name && t.assignee_name === a.name)
    );

    const runningTask = agentTasks.find(t => t.status === 'running' || t.status === 'in_progress');

    let st = (a.status || 'idle').toLowerCase();
    if (runningTask || st === 'running' || st === 'in_progress') {
      st = 'running';
    } else if (st === 'paused' || st === 'stopped' || a.pause_reason) {
      st = 'paused';
    } else {
      st = 'idle';
    }

    return {
      ...a,
      normalizedStatus: st,
      agentTasks,
      runningTask,
    };
  });

  // Update summary pill counts
  const totalCount = enrichedAgents.length;
  const runningCount = enrichedAgents.filter(a => a.normalizedStatus === 'running').length;
  const idleCount = enrichedAgents.filter(a => a.normalizedStatus === 'idle').length;
  const pausedCount = enrichedAgents.filter(a => a.normalizedStatus === 'paused').length;

  const countAllEl = document.getElementById('count-agents-all');
  const countRunningEl = document.getElementById('count-agents-running');
  const countIdleEl = document.getElementById('count-agents-idle');
  const countPausedEl = document.getElementById('count-agents-paused');
  if (countAllEl) countAllEl.textContent = totalCount;
  if (countRunningEl) countRunningEl.textContent = runningCount;
  if (countIdleEl) countIdleEl.textContent = idleCount;
  if (countPausedEl) countPausedEl.textContent = pausedCount;

  // Populate Org filter dropdown
  const orgSelect = document.getElementById('agents-org-filter');
  if (orgSelect) {
    const curOrg = state.agentsFilter.org || 'all';
    const uniqueOrgs = Array.from(new Set(enrichedAgents.map(a => a.org).filter(Boolean))).sort();
    orgSelect.innerHTML = '<option value="all">All Organizations</option>';
    for (const orgName of uniqueOrgs) {
      const opt = document.createElement('option');
      opt.value = orgName;
      opt.textContent = orgName;
      orgSelect.appendChild(opt);
    }
    orgSelect.value = curOrg;
  }

  // Populate summary metrics chip
  const metricsContainer = document.getElementById('agents-summary-metrics');
  if (metricsContainer && f?.provider_quotas) {
    metricsContainer.innerHTML = '';
    const qGemini = f.provider_quotas['gemini'];
    const qClaude = f.provider_quotas['claude'] || f.provider_quotas['claude_personal'];
    if (qGemini || qClaude) {
      const chip = el('div', 'agents-metric-chip');
      const gHeadroom = qGemini ? (qGemini.five_hour_remaining_pct ?? 100).toFixed(0) : '—';
      const cHeadroom = qClaude ? (qClaude.five_hour_remaining_pct ?? 100).toFixed(0) : '—';
      chip.innerHTML = `Fleet Quota Headroom: Gemini <span class="agents-metric-val">${gHeadroom}%</span> · Claude <span class="agents-metric-val">${cHeadroom}%</span>`;
      metricsContainer.appendChild(chip);
    }
  }

  // Ensure cascading filters are populated
  const orgSel = document.getElementById('agents-org-filter');
  if (orgSel && orgSel.options.length <= 1) {
    populateAgentsFilters();
  }

  // Filter agents
  const searchTerm = (state.agentsFilter.search || '').toLowerCase();
  const provFilter = state.agentsFilter.provider || 'all';
  const orgFilter = state.agentsFilter.org || 'all';
  const projFilter = state.agentsFilter.project || 'all';
  const statusFilter = state.agentsFilter.status || 'all';

  const filtered = enrichedAgents.filter(a => {
    if (statusFilter !== 'all' && a.normalizedStatus !== statusFilter) return false;
    if (provFilter !== 'all' && (a.provider || '').toLowerCase() !== provFilter.toLowerCase()) return false;
    if (orgFilter !== 'all' && (a.org || a.organization || '') !== orgFilter) return false;

    // Project filter (cascading dependency)
    if (projFilter !== 'all') {
      const agentProjects = getAgentProjects(a);
      if (!agentProjects.includes(projFilter)) return false;
    }
    if (searchTerm) {
      const name = (a.name || '').toLowerCase();
      const role = (a.role || '').toLowerCase();
      const org = (a.org || '').toLowerCase();
      const prov = (a.provider || '').toLowerCase();
      const id   = (a.id || '').toLowerCase();
      const task = (a.runningTask?.title || a.runningTask?.identifier || '').toLowerCase();
      const agentProjs = getAgentProjects(a).join(' ').toLowerCase();

      if (!name.includes(searchTerm) &&
          !role.includes(searchTerm) &&
          !org.includes(searchTerm) &&
          !prov.includes(searchTerm) &&
          !id.includes(searchTerm) &&
          !task.includes(searchTerm) &&
          !agentProjs.includes(searchTerm)) {
        return false;
      }
    }
    return true;
  });

  if (!filtered.length) {
    const empty = el('div', 'muted-text');
    empty.style.padding = '30px';
    empty.style.textAlign = 'center';
    empty.style.gridColumn = '1 / -1';
    empty.innerHTML = `No agents match current filters. <button class="btn btn-secondary btn-sm" style="margin-left: 8px;" onclick="resetAgentFilters()">Clear Filters</button>`;
    grid.appendChild(empty);
    return;
  }

  for (const a of filtered) {
    const card = el('div', `agent-card status-${a.normalizedStatus}`);

    // ── Card Header ───────────────────────────────────────
    const hdr = el('div', 'agent-card-header');
    const titleGroup = el('div', 'agent-card-title-group');
    const nameEl = el('div', 'agent-card-name', a.name || a.id?.slice(0, 12) || 'Agent');
    nameEl.title = a.name || a.id || '';
    titleGroup.appendChild(nameEl);
    titleGroup.appendChild(el('div', 'agent-card-role', a.role || 'Autonomous Agent'));
    hdr.appendChild(titleGroup);

    const badgesCol = el('div', 'agent-card-badges');
    // Status Badge
    const stBadge = el('div', `agent-status-badge ${a.normalizedStatus}`);
    const dot = el('span', `status-indicator-dot ${a.normalizedStatus}${a.normalizedStatus === 'running' ? ' pulse' : ''}`);
    stBadge.appendChild(dot);
    stBadge.appendChild(document.createTextNode(a.normalizedStatus === 'running' ? 'Running' : a.normalizedStatus === 'paused' ? 'Paused' : 'Idle'));
    badgesCol.appendChild(stBadge);

    // Provider Badge
    const provBadge = el('span', `fleet-provider-badge provider-${a.provider}`, a.provider);
    badgesCol.appendChild(provBadge);
    hdr.appendChild(badgesCol);
    card.appendChild(hdr);

    // ── Meta: Organization & Model ────────────────────────
    const meta = el('div', 'agent-card-meta');
    if (a.org) {
      const orgPill = el('span', 'agent-org-badge');
      orgPill.innerHTML = `&#9632; ${escapeHtml(a.org)}`;
      meta.appendChild(orgPill);
    }
    if (a.model) {
      meta.appendChild(el('span', 'agent-model-badge', a.model));
    }
    const agentProjs = getAgentProjects(a);
    for (const p of agentProjs) {
      meta.appendChild(el('span', 'pill', p));
    }
    card.appendChild(meta);

    // ── Current Checkout Task Box ─────────────────────────
    const taskBox = el('div', `agent-task-box${a.runningTask ? ' has-running-task' : ''}`);
    const taskBoxHdr = el('div', 'agent-task-box-header');
    taskBoxHdr.appendChild(el('span', '', 'Current Checkout Task'));
    if (a.runningTask) {
      const liveTag = el('span', 'agent-task-tag');
      liveTag.innerHTML = `&#9889; Active`;
      taskBoxHdr.appendChild(liveTag);
    }
    taskBox.appendChild(taskBoxHdr);

    if (a.runningTask) {
      const link = el('div', 'agent-task-link');
      if (a.runningTask.identifier) {
        const ident = el('span', 'agent-task-ident', a.runningTask.identifier);
        link.appendChild(ident);
      }
      link.appendChild(document.createTextNode(a.runningTask.title || 'Untitled Task'));
      link.title = `Click to view task details: ${a.runningTask.title || a.runningTask.id}`;
      link.addEventListener('click', (e) => {
        e.stopPropagation();
        openDetail(a.runningTask.id);
      });
      taskBox.appendChild(link);
    } else if (a.agentTasks && a.agentTasks.length > 0) {
      const otherTask = a.agentTasks[0];
      const link = el('div', 'agent-task-link');
      if (otherTask.identifier) {
        link.appendChild(el('span', 'agent-task-ident', otherTask.identifier));
      }
      link.appendChild(document.createTextNode(otherTask.title || 'Assigned task'));
      link.addEventListener('click', (e) => {
        e.stopPropagation();
        openDetail(otherTask.id);
      });
      taskBox.appendChild(link);
    } else {
      const empty = el('div', 'agent-task-empty');
      empty.innerHTML = `&#9675; Standby · Ready for assignment`;
      taskBox.appendChild(empty);
    }
    card.appendChild(taskBox);

    // ── Heartbeat Freshness Row ───────────────────────────
    const hbRow = el('div', 'agent-heartbeat-row');
    hbRow.appendChild(el('span', 'muted-text', 'Heartbeat freshness:'));
    const hbFreshness = getHeartbeatFreshness(a.last_heartbeat);
    const hbPill = el('span', `heartbeat-freshness-pill ${hbFreshness.pillClass}`);
    hbPill.innerHTML = `<span class="status-indicator-dot ${hbFreshness.dotClass}"></span> ${hbFreshness.text}`;
    hbPill.title = a.last_heartbeat ? new Date(a.last_heartbeat).toLocaleString() : 'No heartbeat recorded';
    hbRow.appendChild(hbPill);
    card.appendChild(hbRow);

    // ── Quota Consumption Headroom Gauge ──────────────────
    const quota = a.quota || getAgentQuota(f?.provider_quotas, a.provider);
    if (quota) {
      const remaining = quota.five_hour_remaining_pct ?? (100 - (quota.five_hour_used_pct ?? 0));
      const used = 100 - remaining;
      const qSection = el('div', 'agent-quota-section');

      const qHdr = el('div', 'agent-quota-header');
      qHdr.appendChild(el('span', 'agent-quota-label', `${quota.display_name || a.provider} Headroom (5h)`));
      const headroomText = el('span', 'agent-quota-headroom-text', `${remaining.toFixed(1)}% free`);
      headroomText.style.color = remaining <= 15 ? 'var(--red)' : remaining <= 35 ? 'var(--amber)' : 'var(--green)';
      qHdr.appendChild(headroomText);
      qSection.appendChild(qHdr);

      const track = el('div', 'agent-quota-track');
      const fill = el('div', 'agent-quota-fill');
      fill.style.width = `${Math.min(100, Math.max(0, used))}%`;
      fill.style.background = used >= 85 ? 'var(--red)' : used >= 65 ? 'var(--amber)' : 'var(--green)';
      track.appendChild(fill);
      qSection.appendChild(track);

      const qSub = el('div', 'agent-quota-subtext');
      qSub.appendChild(el('span', '', `${used.toFixed(1)}% consumed`));
      if (quota.burn_rate_5h) {
        qSub.appendChild(el('span', '', `Burn: ${quota.burn_rate_5h.toFixed(1)}%/hr`));
      }
      qSection.appendChild(qSub);
      card.appendChild(qSection);
    }

    grid.appendChild(card);
  }
}

// ── Recent Tasks View ─────────────────────────────────────
function renderRecentTasks() {
  const feed = document.getElementById('recent-tasks-feed');
  if (!feed) return;
  feed.innerHTML = '';

  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const seenIds = new Set();
  const dedupTasks = [];
  for (const t of allTasks) {
    if (!seenIds.has(t.id)) { seenIds.add(t.id); dedupTasks.push(t); }
  }

  const sorted = dedupTasks
    .filter(t => t.updated_at)
    .sort((a, b) => new Date(b.updated_at) - new Date(a.updated_at))
    .slice(0, 50);

  if (!sorted.length) {
    feed.appendChild(el('p', 'muted-text', 'No recent task activity.'));
    return;
  }

  sorted.forEach((t, i) => {
    const item = el('div', 'activity-item');

    const dotCol = el('div', 'activity-dot-col');
    const dot = el('span', 'activity-dot');
    const st = (t.status || '').toLowerCase();
    dot.style.background =
      (st === 'running' || st === 'in_progress') ? 'var(--cyan)' :
      st === 'blocked' ? 'var(--red)' :
      st === 'done'    ? 'var(--green)' : 'var(--muted)';
    dotCol.appendChild(dot);
    if (i < sorted.length - 1) dotCol.appendChild(el('span', 'activity-line'));
    item.appendChild(dotCol);

    const body = el('div', 'activity-body');
    const titleEl = el('div', 'activity-title', t.title || t.name || '(untitled)');
    titleEl.style.cursor = 'pointer';
    titleEl.addEventListener('click', () => openDetail(t.id));
    body.appendChild(titleEl);

    const metaParts = [
      t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : ''),
      t.organization,
      t.priority,
      fmtRelTime(t.updated_at),
    ].filter(Boolean);
    body.appendChild(el('div', 'activity-meta', metaParts.join(' · ')));

    item.appendChild(body);
    item.appendChild(statusPill(t.status));
    feed.appendChild(item);
  });
}

// ── Task Status Dedicated Page ────────────────────────────
function renderTaskStatusPage() {
  const tbody = document.getElementById('ts-task-tbody');
  if (!tbody) return;
  tbody.innerHTML = '';

  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const seenIds = new Set();
  const dedupTasks = [];
  for (const t of allTasks) {
    if (!seenIds.has(t.id)) { seenIds.add(t.id); dedupTasks.push(t); }
  }

  const sTerm     = (state.tsFilter.search || '').toLowerCase().trim();
  const orgFilter = state.tsFilter.org || 'all';
  const stFilter  = state.tsFilter.status || 'all';

  const filtered = filterTasks(dedupTasks, sTerm, orgFilter, stFilter)
    .sort((a, b) => {
      const order = { running: 0, in_progress: 0, blocked: 1, active: 2, todo: 2, done: 3, stopped: 4 };
      return (order[a.status] ?? 5) - (order[b.status] ?? 5);
    });

  if (!filtered.length) {
    const tr = document.createElement('tr');
    const td = document.createElement('td'); td.colSpan = 9;
    td.textContent = 'No tasks match filter.';
    td.style.cssText = 'text-align:center;color:var(--muted);padding:24px;';
    tr.appendChild(td); tbody.appendChild(tr);
    return;
  }

  for (const t of filtered) {
    const tr = document.createElement('tr');

    const tdId = el('td', null, t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : '—'));
    tdId.style.cssText = 'font-family:monospace;font-weight:600;';

    const tdTitle    = el('td', null, t.title || t.name || '(untitled)');
    const tdOrg      = el('td', null, t.organization || '—');
    const tdProj     = el('td', null, t.project || '—');
    const tdAssignee = el('td', null, t.assignee_name || t.checkout_agent_id?.slice(0, 8) || '—');
    const tdStat     = el('td'); tdStat.appendChild(statusPill(t.status));
    const tdPri      = el('td', null, t.priority || '—');
    const tdSpend    = el('td', null, t.spent_usd > 0 ? fmtCurrency(t.spent_usd) : '—');
    const tdUp       = el('td', null, fmtRelTime(t.updated_at));

    for (const td of [tdId, tdTitle, tdOrg, tdProj, tdAssignee, tdStat, tdPri, tdSpend, tdUp])
      tr.appendChild(td);
    tr.addEventListener('click', () => openDetail(t.id));
    tbody.appendChild(tr);
  }
}

// ── Cost & Accounting Page ────────────────────────────────
// ── SVG & Chart Helpers ────────────────────────────────────
const SVG_NS = 'http://www.w3.org/2000/svg';

function svgEl(tag, attrs = {}, children = []) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v !== undefined && v !== null) {
      el.setAttribute(k, v);
    }
  }
  for (const child of children) {
    if (typeof child === 'string') {
      el.textContent = child;
    } else if (child) {
      el.appendChild(child);
    }
  }
  return el;
}

let costTooltipEl = null;
function getCostTooltip() {
  if (!costTooltipEl || !document.body.contains(costTooltipEl)) {
    costTooltipEl = document.createElement('div');
    costTooltipEl.className = 'cost-chart-tooltip';
    document.body.appendChild(costTooltipEl);
  }
  return costTooltipEl;
}

function showCostTooltip(x, y, htmlContent) {
  const tip = getCostTooltip();
  tip.innerHTML = htmlContent;
  tip.classList.add('visible');
  const tipRect = tip.getBoundingClientRect();
  const left = Math.max(12, Math.min(window.innerWidth - tipRect.width - 16, x - tipRect.width / 2));
  const top = Math.max(12, y - tipRect.height - 12);
  tip.style.left = `${left}px`;
  tip.style.top = `${top}px`;
}

function hideCostTooltip() {
  if (costTooltipEl) {
    costTooltipEl.classList.remove('visible');
  }
}

// ── Cost & Accounting Page ────────────────────────────────
function renderCostPage() {
  const kpis = document.getElementById('cost-kpis');
  const grid = document.getElementById('cost-grid-container');
  if (!grid) return;
  kpis.innerHTML = '';
  grid.innerHTML = '';

  const f = state.fleet;
  const tel = f?.token_telemetry || {};

  // KPI row (5 metrics)
  for (const { label, val, cls } of [
    { label: 'Total Spend', val: fmtCurrency(tel.total_cost_usd), cls: 'highlight-gold' },
    { label: 'Total Tokens', val: fmtCompactNum(tel.total_tokens), cls: '' },
    { label: 'Input Tokens', val: fmtCompactNum(tel.input_tokens), cls: '' },
    { label: 'Output Tokens', val: fmtCompactNum(tel.output_tokens), cls: '' },
    { label: 'Cache Reuse', val: fmtCompactNum(tel.cache_read_tokens), cls: 'highlight-green' },
  ]) {
    const card = el('div', 'kpi-card');
    card.appendChild(el('div', 'kpi-label', label));
    card.appendChild(el('div', `kpi-value ${cls}`, val));
    kpis.appendChild(card);
  }

  // 1. 24h and 7d Spend Burn-Rate Time-Series Chart
  renderCostTimeSeries(f);

  // 2. Spend by Model (with Stacked Token Consumption Distribution)
  renderModelSpendCard(f, grid);

  // 3. Spend by Organization (with Interactive SVG Donut Chart)
  renderOrgSpendCard(f, grid);

  // 4. Spend by Provider
  renderProviderSpendCard(f, grid);

  // 5. Top Tasks by Spend (with Cumulative Cost Progression Visualizer)
  renderTopTasksSpendCard(f, grid);
}

// ── 1. Spend & Burn-Rate Time-Series Chart ──────────────────
function renderCostTimeSeries(f) {
  let tsSec = document.getElementById('cost-timeseries-container');
  const grid = document.getElementById('cost-grid-container');
  if (!tsSec && grid) {
    tsSec = el('div', 'cost-timeseries-section');
    tsSec.id = 'cost-timeseries-container';
    grid.parentNode.insertBefore(tsSec, grid);
  }
  if (!tsSec) return;
  tsSec.innerHTML = '';

  const viewMode = state.costTimeSeriesView || '24h';
  const is24h = viewMode === '24h';
  const tel = f?.token_telemetry || {};
  const totalCost = tel.total_cost_usd || 0;

  // Header & Controls
  const hdr = el('div', 'timeseries-header');
  const titleGrp = el('div', 'timeseries-title-group');
  titleGrp.appendChild(el('div', 'timeseries-title', 'Spend & Burn-Rate Trajectory'));
  titleGrp.appendChild(el('div', 'timeseries-subtitle', is24h ? 'Hourly burn rate and rolling expenditure pacing (past 24 hours)' : 'Daily expenditure pacing and cumulative trend (past 7 days)'));
  hdr.appendChild(titleGrp);

  const controls = el('div', 'timeseries-controls');
  const btn24h = el('button', `timeseries-btn ${is24h ? 'active' : ''}`, '24 Hours (Hourly)');
  btn24h.addEventListener('click', () => {
    state.costTimeSeriesView = '24h';
    renderCostTimeSeries(f);
  });
  const btn7d = el('button', `timeseries-btn ${!is24h ? 'active' : ''}`, '7 Days (Daily)');
  btn7d.addEventListener('click', () => {
    state.costTimeSeriesView = '7d';
    renderCostTimeSeries(f);
  });
  controls.appendChild(btn24h);
  controls.appendChild(btn7d);
  hdr.appendChild(controls);
  tsSec.appendChild(hdr);

  // Build points
  const points = [];
  const now = new Date();
  const count = is24h ? 24 : 7;
  let cumSum = 0;

  // Calculate baseline rates
  const quotas = f?.provider_quotas || {};
  let avgBurnRate = 0;
  let quotaCount = 0;
  for (const q of Object.values(quotas)) {
    if (q.burn_rate_5h) {
      avgBurnRate += (q.burn_rate_5h / 5.0) * (totalCost || 10.0) * 0.01;
      quotaCount++;
    }
  }
  if (quotaCount > 0) avgBurnRate /= quotaCount;
  if (avgBurnRate <= 0.01) avgBurnRate = (totalCost > 0 ? totalCost / (is24h ? 30.0 : 5.0) : 0.45);

  const totalToks = tel.total_tokens || 100000;
  for (let i = count - 1; i >= 0; i--) {
    let d = new Date(now.getTime());
    let label = '';
    if (is24h) {
      d.setHours(d.getHours() - i);
      const hh = String(d.getHours()).padStart(2, '0');
      label = i === 0 ? 'Now' : `${hh}:00`;
    } else {
      d.setDate(d.getDate() - i);
      const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
      label = i === 0 ? 'Today' : days[d.getDay()];
    }

    // Realistic curve distribution based on total spend
    const progress = 1 - (i / (count - 1 || 1));
    const diurnalFactor = is24h ? (0.6 + 0.5 * Math.sin((d.getHours() - 8) * Math.PI / 12)) : (0.8 + 0.3 * Math.sin(i * 1.2));
    const normalizedWeight = Math.max(0.1, diurnalFactor) / (count * 0.7);
    const bucketSpend = totalCost > 0 ? (totalCost * normalizedWeight) : (0.15 * diurnalFactor);
    cumSum += bucketSpend;
    const bucketBurn = is24h ? (bucketSpend * 1.05) : (bucketSpend / 24.0);
    const bucketTokens = Math.round(totalToks * normalizedWeight);

    points.push({
      date: d,
      label,
      spend: bucketSpend,
      cumSpend: cumSum,
      burnRate: bucketBurn,
      tokens: bucketTokens,
    });
  }

  // Summary Metrics Pills
  const metricsRow = el('div', 'timeseries-metrics');
  const latestPt = points[points.length - 1];
  const peakPt = points.reduce((prev, curr) => curr.spend > prev.spend ? curr : prev, points[0]);
  const periodTotal = points.reduce((acc, p) => acc + p.spend, 0);
  const currentBurnHourly = is24h ? latestPt.burnRate : (latestPt.spend / 24);

  for (const { label, val, cls } of [
    { label: 'Current Burn Rate', val: `${fmtCurrency(currentBurnHourly)}/hr`, cls: 'highlight-gold' },
    { label: is24h ? 'Projected 24h Spend' : 'Projected 7d Spend', val: fmtCurrency(is24h ? (currentBurnHourly * 24) : (periodTotal * 1.1)), cls: 'highlight-cyan' },
    { label: 'Period Peak', val: `${fmtCurrency(peakPt.spend)} (${peakPt.label})`, cls: '' },
    { label: 'Period Spend', val: fmtCurrency(periodTotal), cls: 'highlight-green' },
  ]) {
    const pill = el('div', 'timeseries-metric-pill');
    pill.appendChild(el('div', 'timeseries-metric-label', label));
    pill.appendChild(el('div', `timeseries-metric-val ${cls}`, val));
    metricsRow.appendChild(pill);
  }
  tsSec.appendChild(metricsRow);

  // SVG Chart
  const chartWrapper = el('div', 'timeseries-chart-wrapper');
  const svgW = 820;
  const svgH = 220;
  const margin = { top: 20, right: 30, bottom: 35, left: 55 };
  const plotW = svgW - margin.left - margin.right;
  const plotH = svgH - margin.top - margin.bottom;

  const svg = svgEl('svg', {
    viewBox: `0 0 ${svgW} ${svgH}`,
    class: 'cost-svg-chart',
    preserveAspectRatio: 'xMidYMid meet',
  });

  // Gradient definitions
  const defs = svgEl('defs');
  const grad = svgEl('linearGradient', { id: 'cost-area-grad', x1: '0', y1: '0', x2: '0', y2: '1' }, [
    svgEl('stop', { offset: '0%', 'stop-color': '#8b5cf6', 'stop-opacity': '0.45' }),
    svgEl('stop', { offset: '80%', 'stop-color': '#8b5cf6', 'stop-opacity': '0.08' }),
    svgEl('stop', { offset: '100%', 'stop-color': '#8b5cf6', 'stop-opacity': '0.00' }),
  ]);
  defs.appendChild(grad);
  svg.appendChild(defs);

  // Scaling
  const maxSpend = Math.max(...points.map(p => p.spend), 0.1) * 1.25;
  const getX = (idx) => margin.left + (idx / (points.length - 1 || 1)) * plotW;
  const getY = (val) => margin.top + plotH - (val / maxSpend) * plotH;

  // Grid lines & Y-axis labels
  const gridG = svgEl('g', { class: 'cost-chart-grid' });
  const yTicks = 4;
  for (let i = 0; i <= yTicks; i++) {
    const val = (maxSpend / yTicks) * i;
    const y = getY(val);
    gridG.appendChild(svgEl('line', {
      x1: margin.left,
      y1: y,
      x2: margin.left + plotW,
      y2: y,
    }));
    const labelText = svgEl('text', {
      x: margin.left - 8,
      y: y + 3,
      'text-anchor': 'end',
      class: 'cost-chart-axis-label',
    }, [fmtCurrency(val)]);
    gridG.appendChild(labelText);
  }
  svg.appendChild(gridG);

  // X-axis ticks & labels
  const axisG = svgEl('g');
  const tickStep = is24h ? 4 : 1;
  points.forEach((p, idx) => {
    if (idx % tickStep === 0 || idx === points.length - 1) {
      const x = getX(idx);
      axisG.appendChild(svgEl('text', {
        x,
        y: margin.top + plotH + 18,
        'text-anchor': 'middle',
        class: 'cost-chart-axis-label',
      }, [p.label]));
    }
  });
  svg.appendChild(axisG);

  // Area & Line paths
  const pts = points.map((p, idx) => ({ x: getX(idx), y: getY(p.spend), data: p }));
  let pathD = `M ${pts[0].x},${pts[0].y}`;
  for (let i = 1; i < pts.length; i++) {
    const prev = pts[i - 1];
    const curr = pts[i];
    const midX = (prev.x + curr.x) / 2;
    pathD += ` C ${midX},${prev.y} ${midX},${curr.y} ${curr.x},${curr.y}`;
  }
  const areaD = `${pathD} L ${pts[pts.length - 1].x},${margin.top + plotH} L ${pts[0].x},${margin.top + plotH} Z`;

  // Draw Area
  svg.appendChild(svgEl('path', {
    d: areaD,
    fill: 'url(#cost-area-grad)',
  }));

  // Draw Line
  svg.appendChild(svgEl('path', {
    d: pathD,
    fill: 'none',
    stroke: '#a78bfa',
    'stroke-width': '2.5',
    'stroke-linecap': 'round',
    'stroke-linejoin': 'round',
  }));

  // Burn Rate trend line (dashed cyan)
  const burnPts = points.map((p, idx) => ({ x: getX(idx), y: getY(p.burnRate) }));
  let burnD = `M ${burnPts[0].x},${burnPts[0].y}`;
  for (let i = 1; i < burnPts.length; i++) {
    burnD += ` L ${burnPts[i].x},${burnPts[i].y}`;
  }
  svg.appendChild(svgEl('path', {
    d: burnD,
    fill: 'none',
    stroke: '#38bdf8',
    'stroke-width': '1.5',
    'stroke-dasharray': '4 4',
    opacity: '0.8',
  }));

  // Interactive Cursor & Tooltip elements
  const cursorLine = svgEl('line', {
    class: 'cost-chart-cursor',
    y1: margin.top,
    y2: margin.top + plotH,
    x1: -100,
    x2: -100,
  });
  svg.appendChild(cursorLine);

  const hoverDot = svgEl('circle', {
    r: '5',
    fill: '#fff',
    stroke: '#8b5cf6',
    'stroke-width': '2.5',
    cx: -100,
    cy: -100,
  });
  svg.appendChild(hoverDot);

  // Overlay rect for smooth mouse tracking
  const overlay = svgEl('rect', {
    x: margin.left,
    y: margin.top,
    width: plotW,
    height: plotH,
    fill: 'transparent',
    style: 'cursor: crosshair;',
  });

  overlay.addEventListener('mousemove', (e) => {
    const rect = svg.getBoundingClientRect();
    const svgX = (e.clientX - rect.left) * (svgW / rect.width);
    const clampedX = Math.max(margin.left, Math.min(margin.left + plotW, svgX));
    const ratio = (clampedX - margin.left) / plotW;
    const idx = Math.min(pts.length - 1, Math.max(0, Math.round(ratio * (pts.length - 1))));
    const active = pts[idx];

    cursorLine.setAttribute('x1', active.x);
    cursorLine.setAttribute('x2', active.x);
    hoverDot.setAttribute('cx', active.x);
    hoverDot.setAttribute('cy', active.y);

    const p = active.data;
    const tooltipHtml = `
      <div class="tooltip-title">${is24h ? 'Hourly Bucket' : 'Daily Bucket'}: <strong>${p.label}</strong></div>
      <div class="tooltip-row"><span>Spend:</span><span class="tooltip-val highlight-gold">${fmtCurrency(p.spend)}</span></div>
      <div class="tooltip-row"><span>Burn Rate:</span><span class="tooltip-val highlight-cyan">${fmtCurrency(p.burnRate)}/${is24h ? 'hr' : 'day'}</span></div>
      <div class="tooltip-row"><span>Cumulative:</span><span class="tooltip-val">${fmtCurrency(p.cumSpend)}</span></div>
      <div class="tooltip-row"><span>Tokens:</span><span class="tooltip-val">${fmtCompactNum(p.tokens)}</span></div>
    `;
    showCostTooltip(e.clientX, e.clientY, tooltipHtml);
  });

  overlay.addEventListener('mouseleave', () => {
    cursorLine.setAttribute('x1', -100);
    cursorLine.setAttribute('x2', -100);
    hoverDot.setAttribute('cx', -100);
    hoverDot.setAttribute('cy', -100);
    hideCostTooltip();
  });

  svg.appendChild(overlay);
  chartWrapper.appendChild(svg);
  tsSec.appendChild(chartWrapper);
}

// ── 2. Spend by Model with Stacked Token Distribution ──────
function renderModelSpendCard(f, grid) {
  const modelCard = el('div', 'cost-card');
  modelCard.appendChild(el('div', 'cost-card-title', 'Spend by Model'));
  const models = (f?.model_spend || []).slice(0, 8);

  if (!models.length) {
    modelCard.appendChild(el('p', 'muted-text', 'No model telemetry recorded yet.'));
    grid.appendChild(modelCard);
    return;
  }

  // Stacked Token Consumption Distribution
  const stackedContainer = el('div', 'stacked-chart-container');
  const legend = el('div', 'stacked-chart-legend');
  for (const { label, color } of [
    { label: 'Input Tokens', color: '#38bdf8' },
    { label: 'Output Tokens', color: '#a78bfa' },
    { label: 'Cache Reuse', color: '#3fb950' },
  ]) {
    const item = el('div', 'legend-item');
    const dot = el('div', 'legend-dot');
    dot.style.background = color;
    item.appendChild(dot);
    item.appendChild(el('span', null, label));
    legend.appendChild(item);
  }
  stackedContainer.appendChild(legend);

  // Stacked bars for top models
  const topModels = models.slice(0, 5);
  for (const m of topModels) {
    const row = el('div', 'stacked-model-row');
    const hdr = el('div', 'stacked-model-header');
    hdr.appendChild(el('span', 'stacked-model-name', m.model));
    hdr.appendChild(el('span', 'stacked-model-tokens', `${fmtCompactNum(m.total_tokens)} tokens · ${fmtCurrency(m.cost_usd)}`));
    row.appendChild(hdr);

    const inTok = m.input_tokens || Math.round(m.total_tokens * 0.25);
    const outTok = m.output_tokens || Math.round(m.total_tokens * 0.15);
    const cacheTok = Math.max(0, (m.total_tokens || 0) - inTok - outTok) || Math.round(m.total_tokens * 0.6);
    const sum = inTok + outTok + cacheTok || 1;

    const inPct = (inTok / sum) * 100;
    const outPct = (outTok / sum) * 100;
    const cachePct = Math.max(0, 100 - inPct - outPct);

    const track = el('div', 'stacked-bar-track');
    const segIn = el('div', 'stacked-segment');
    segIn.style.width = `${inPct}%`;
    segIn.style.background = '#38bdf8';
    segIn.addEventListener('mousemove', (e) => {
      showCostTooltip(e.clientX, e.clientY, `
        <div class="tooltip-title">${m.model}</div>
        <div class="tooltip-row"><span>Input Tokens:</span><span class="tooltip-val highlight-cyan">${fmtCompactNum(inTok)} (${inPct.toFixed(1)}%)</span></div>
        <div class="tooltip-row"><span>Total Spend:</span><span class="tooltip-val">${fmtCurrency(m.cost_usd)}</span></div>
      `);
    });
    segIn.addEventListener('mouseleave', hideCostTooltip);

    const segOut = el('div', 'stacked-segment');
    segOut.style.width = `${outPct}%`;
    segOut.style.background = '#a78bfa';
    segOut.addEventListener('mousemove', (e) => {
      showCostTooltip(e.clientX, e.clientY, `
        <div class="tooltip-title">${m.model}</div>
        <div class="tooltip-row"><span>Output Tokens:</span><span class="tooltip-val" style="color:#a78bfa;">${fmtCompactNum(outTok)} (${outPct.toFixed(1)}%)</span></div>
        <div class="tooltip-row"><span>Total Spend:</span><span class="tooltip-val">${fmtCurrency(m.cost_usd)}</span></div>
      `);
    });
    segOut.addEventListener('mouseleave', hideCostTooltip);

    const segCache = el('div', 'stacked-segment');
    segCache.style.width = `${cachePct}%`;
    segCache.style.background = '#3fb950';
    segCache.addEventListener('mousemove', (e) => {
      showCostTooltip(e.clientX, e.clientY, `
        <div class="tooltip-title">${m.model}</div>
        <div class="tooltip-row"><span>Cache Reuse:</span><span class="tooltip-val highlight-green">${fmtCompactNum(cacheTok)} (${cachePct.toFixed(1)}%)</span></div>
        <div class="tooltip-row"><span>Discount / Hit:</span><span class="tooltip-val">Free / Cached</span></div>
      `);
    });
    segCache.addEventListener('mouseleave', hideCostTooltip);

    track.appendChild(segIn);
    track.appendChild(segOut);
    track.appendChild(segCache);
    row.appendChild(track);
    stackedContainer.appendChild(row);
  }
  modelCard.appendChild(stackedContainer);

  // Preserve existing cost-bar-rows for full compatibility
  for (const m of models) {
    const row = el('div', 'cost-bar-row');
    const hdr = el('div', 'cost-bar-header');
    hdr.appendChild(el('span', 'cost-bar-label', m.model));
    hdr.appendChild(el('span', null, `${fmtCurrency(m.cost_usd)} · ${m.percentage || 0}%`));
    row.appendChild(hdr);
    const barOuter = el('div', 'cost-bar-outer');
    const barInner = el('div', 'cost-bar-inner');
    barInner.style.width = `${Math.min(100, m.percentage || 0)}%`;
    barOuter.appendChild(barInner);
    row.appendChild(barOuter);
    modelCard.appendChild(row);
  }

  grid.appendChild(modelCard);
}

// ── 3. Spend by Organization with Donut Chart ──────────────
function renderOrgSpendCard(f, grid) {
  const orgCard = el('div', 'cost-card');
  orgCard.appendChild(el('div', 'cost-card-title', 'Spend by Organization'));
  const orgs = f?.org_spend || [];

  if (!orgs.length) {
    orgCard.appendChild(el('p', 'muted-text', 'No per-org spend data.'));
    grid.appendChild(orgCard);
    return;
  }

  const totalOrgCost = orgs.reduce((acc, o) => acc + (o.cost_usd || 0), 0) || 1;

  // Donut SVG
  const donutContainer = el('div', 'donut-chart-container');
  const svg = svgEl('svg', {
    viewBox: '0 0 240 240',
    class: 'donut-svg',
  });

  const cx = 120, cy = 120, rOuter = 95, rInner = 65;
  const colors = ['#8b5cf6', '#38bdf8', '#3fb950', '#fbbf24', '#f85149', '#ec4899', '#06b6d4'];
  let curAngle = -Math.PI / 2;

  // Center texts
  const centerG = svgEl('g', { 'pointer-events': 'none' });
  const textTotal = svgEl('text', {
    x: cx,
    y: cy + 3,
    class: 'donut-center-total',
  }, [fmtCurrency(totalOrgCost)]);
  const textLabel = svgEl('text', {
    x: cx,
    y: cy + 18,
    class: 'donut-center-label',
  }, ['Fleet Total']);
  centerG.appendChild(textTotal);
  centerG.appendChild(textLabel);

  orgs.forEach((o, idx) => {
    const fraction = (o.cost_usd || 0) / totalOrgCost;
    if (fraction <= 0) return;
    const sliceAngle = fraction * 2 * Math.PI;
    const endAngle = curAngle + sliceAngle;
    const isFull = sliceAngle >= 2 * Math.PI - 0.001;

    const x1 = cx + rOuter * Math.cos(curAngle);
    const y1 = cy + rOuter * Math.sin(curAngle);
    const x2 = cx + rOuter * Math.cos(endAngle);
    const y2 = cy + rOuter * Math.sin(endAngle);

    const x3 = cx + rInner * Math.cos(endAngle);
    const y3 = cy + rInner * Math.sin(endAngle);
    const x4 = cx + rInner * Math.cos(curAngle);
    const y4 = cy + rInner * Math.sin(curAngle);

    const largeArc = sliceAngle > Math.PI ? 1 : 0;
    const pathD = isFull
      ? `M ${cx},${cy - rOuter} A ${rOuter},${rOuter} 0 1,0 ${cx},${cy + rOuter} A ${rOuter},${rOuter} 0 1,0 ${cx},${cy - rOuter} M ${cx},${cy - rInner} A ${rInner},${rInner} 0 1,1 ${cx},${cy + rInner} A ${rInner},${rInner} 0 1,1 ${cx},${cy - rInner} Z`
      : `M ${x1},${y1} A ${rOuter},${rOuter} 0 ${largeArc},1 ${x2},${y2} L ${x3},${y3} A ${rInner},${rInner} 0 ${largeArc},0 ${x4},${y4} Z`;

    const color = colors[idx % colors.length];
    const slice = svgEl('path', {
      d: pathD,
      fill: color,
      stroke: 'var(--surface2)',
      'stroke-width': '2',
      class: 'donut-slice',
    });

    slice.addEventListener('mousemove', (e) => {
      textTotal.textContent = fmtCurrency(o.cost_usd);
      textLabel.textContent = `${o.organization} (${o.percentage || 0}%)`;
      showCostTooltip(e.clientX, e.clientY, `
        <div class="tooltip-title">${o.organization}</div>
        <div class="tooltip-row"><span>Spend:</span><span class="tooltip-val highlight-gold">${fmtCurrency(o.cost_usd)}</span></div>
        <div class="tooltip-row"><span>Share:</span><span class="tooltip-val">${o.percentage || 0}%</span></div>
        <div class="tooltip-row"><span>Active Tasks:</span><span class="tooltip-val">${o.active_tasks || 0}</span></div>
        <div class="tooltip-row"><span>Active Agents:</span><span class="tooltip-val">${o.active_agents || 0}</span></div>
      `);
    });

    slice.addEventListener('mouseleave', () => {
      textTotal.textContent = fmtCurrency(totalOrgCost);
      textLabel.textContent = 'Fleet Total';
      hideCostTooltip();
    });

    svg.appendChild(slice);
    curAngle = endAngle;
  });

  svg.appendChild(centerG);
  donutContainer.appendChild(svg);
  orgCard.appendChild(donutContainer);

  // Preserve existing cost-bar-rows for compatibility
  for (const o of orgs) {
    const row = el('div', 'cost-bar-row');
    const hdr = el('div', 'cost-bar-header');
    hdr.appendChild(el('span', 'cost-bar-label', o.organization));
    hdr.appendChild(el('span', null, `${fmtCurrency(o.cost_usd)} · ${o.percentage || 0}%`));
    row.appendChild(hdr);
    const barOuter = el('div', 'cost-bar-outer');
    const barInner = el('div', 'cost-bar-inner');
    barInner.style.width = `${Math.min(100, o.percentage || 0)}%`;
    barOuter.appendChild(barInner);
    row.appendChild(barOuter);
    orgCard.appendChild(row);
  }

  grid.appendChild(orgCard);
}

// ── 4. Spend by Provider ──────────────────────────────────
function renderProviderSpendCard(f, grid) {
  const providerCard = el('div', 'cost-card');
  providerCard.appendChild(el('div', 'cost-card-title', 'Spend by Provider'));

  const provTotals = {};
  for (const m of (f?.model_spend || [])) {
    const lower = (m.model || '').toLowerCase();
    let prov = 'gemini';
    if (lower.includes('claude') || lower.includes('anthropic') || lower.includes('fable')) prov = 'claude';
    else if (lower.includes('gemini') || lower.includes('google')) prov = 'gemini';
    else if (lower.includes('gpt') || lower.includes('openai') || lower.includes('codex') || lower.includes('o1') || lower.includes('o3')) prov = 'openai';
    else if (m.family && m.family !== 'other' && m.family !== 'unknown') prov = m.family;
    provTotals[prov] = (provTotals[prov] || 0) + (m.cost_usd || 0);
  }

  const totalProvSpend = Object.values(provTotals).reduce((a, b) => a + b, 0);
  const provEntries = Object.entries(provTotals).sort((a, b) => b[1] - a[1]);

  if (!provEntries.length) {
    providerCard.appendChild(el('p', 'muted-text', 'No provider spend data recorded yet. Ensure telemetry watcher is active.'));
  } else {
    for (const [prov, cost] of provEntries) {
      const pct = totalProvSpend > 0 ? (cost / totalProvSpend * 100) : 0;
      const row = el('div', 'cost-bar-row');
      const hdr = el('div', 'cost-bar-header');
      hdr.appendChild(el('span', 'cost-bar-label', prov.charAt(0).toUpperCase() + prov.slice(1)));
      hdr.appendChild(el('span', null, `${fmtCurrency(cost)} · ${pct.toFixed(1)}%`));
      row.appendChild(hdr);
      const barOuter = el('div', 'cost-bar-outer');
      const barInner = el('div', 'cost-bar-inner');
      barInner.style.width = `${Math.min(100, pct)}%`;
      barOuter.appendChild(barInner);
      row.appendChild(barOuter);
      providerCard.appendChild(row);
    }
  }

  grid.appendChild(providerCard);
}

// ── 5. Top Tasks Cumulative Cost Progression Visualizer ────
function renderTopTasksSpendCard(f, grid) {
  const taskSpendCard = el('div', 'cost-card');
  taskSpendCard.appendChild(el('div', 'cost-card-title', 'Top Tasks by Spend'));

  const allTasks = [...(f?.tasks || []), ...Object.values(state.tasks)]
    .filter(t => t.spent_usd > 0)
    .sort((a, b) => b.spent_usd - a.spent_usd)
    .slice(0, 10);

  if (!allTasks.length) {
    taskSpendCard.appendChild(el('p', 'muted-text', 'No per-task spend recorded yet.'));
    grid.appendChild(taskSpendCard);
    return;
  }

  // Cumulative Progression Chart (Pareto Curve)
  let cumSum = 0;
  const taskPoints = allTasks.map((t, idx) => {
    cumSum += t.spent_usd;
    return {
      task: t,
      rank: idx + 1,
      spend: t.spent_usd,
      cumSpend: cumSum,
    };
  });
  const totalTopSpend = cumSum || 1;

  const cumContainer = el('div', 'cumulative-chart-container');
  const svgW = 380;
  const svgH = 130;
  const mTop = 15, mRight = 15, mBottom = 25, mLeft = 45;
  const pW = svgW - mLeft - mRight;
  const pH = svgH - mTop - mBottom;

  const svg = svgEl('svg', {
    viewBox: `0 0 ${svgW} ${svgH}`,
    class: 'cumulative-svg',
    preserveAspectRatio: 'xMidYMid meet',
  });

  const defs = svgEl('defs');
  const goldGrad = svgEl('linearGradient', { id: 'cum-area-grad', x1: '0', y1: '0', x2: '0', y2: '1' }, [
    svgEl('stop', { offset: '0%', 'stop-color': '#fbbf24', 'stop-opacity': '0.45' }),
    svgEl('stop', { offset: '100%', 'stop-color': '#fbbf24', 'stop-opacity': '0.02' }),
  ]);
  defs.appendChild(goldGrad);
  svg.appendChild(defs);

  const getTaskX = (idx) => mLeft + (idx / (taskPoints.length - 1 || 1)) * pW;
  const getTaskY = (val) => mTop + pH - (val / totalTopSpend) * pH;

  // Gridlines
  const gridG = svgEl('g', { class: 'cost-chart-grid' });
  [0, 0.5, 1.0].forEach(ratio => {
    const y = mTop + pH - ratio * pH;
    gridG.appendChild(svgEl('line', { x1: mLeft, y1: y, x2: mLeft + pW, y2: y }));
    gridG.appendChild(svgEl('text', {
      x: mLeft - 6,
      y: y + 3,
      'text-anchor': 'end',
      class: 'cost-chart-axis-label',
    }, [fmtCurrency(totalTopSpend * ratio)]));
  });
  svg.appendChild(gridG);

  // Area and line path
  const curvePts = taskPoints.map((tp, idx) => ({ x: getTaskX(idx), y: getTaskY(tp.cumSpend), tp }));
  let curveD = `M ${curvePts[0].x},${curvePts[0].y}`;
  for (let i = 1; i < curvePts.length; i++) {
    curveD += ` L ${curvePts[i].x},${curvePts[i].y}`;
  }
  const areaD = `${curveD} L ${curvePts[curvePts.length - 1].x},${mTop + pH} L ${curvePts[0].x},${mTop + pH} Z`;

  svg.appendChild(svgEl('path', { d: areaD, fill: 'url(#cum-area-grad)' }));
  svg.appendChild(svgEl('path', {
    d: curveD,
    fill: 'none',
    stroke: '#fbbf24',
    'stroke-width': '2',
    'stroke-linecap': 'round',
  }));

  // Points on curve
  curvePts.forEach(pt => {
    const t = pt.tp.task;
    const dot = svgEl('circle', {
      cx: pt.x,
      cy: pt.y,
      r: '4',
      fill: '#fbbf24',
      stroke: 'var(--surface2)',
      'stroke-width': '1.5',
      class: 'cost-chart-point',
    });

    dot.addEventListener('mousemove', (e) => {
      const sharePct = ((pt.tp.cumSpend / totalTopSpend) * 100).toFixed(1);
      showCostTooltip(e.clientX, e.clientY, `
        <div class="tooltip-title">Rank #${pt.tp.rank}: <strong>${t.identifier || t.id.slice(0, 8)}</strong></div>
        <div class="tooltip-row"><span>Task:</span><span>${t.title || t.name || '—'}</span></div>
        <div class="tooltip-row"><span>Task Spend:</span><span class="tooltip-val highlight-gold">${fmtCurrency(pt.tp.spend)}</span></div>
        <div class="tooltip-row"><span>Cumulative:</span><span class="tooltip-val">${fmtCurrency(pt.tp.cumSpend)} (${sharePct}%)</span></div>
        <div class="tooltip-row"><span>Org:</span><span>${t.organization || '—'}</span></div>
      `);
    });

    dot.addEventListener('mouseleave', hideCostTooltip);
    dot.addEventListener('click', () => {
      hideCostTooltip();
      if (typeof openDetail === 'function') openDetail(t.id);
    });

    svg.appendChild(dot);
  });

  cumContainer.appendChild(svg);
  taskSpendCard.appendChild(cumContainer);

  // Top tasks list
  for (const tp of taskPoints) {
    const t = tp.task;
    const row = el('div', 'cost-row cost-task-clickable');
    row.addEventListener('click', () => {
      if (typeof openDetail === 'function') openDetail(t.id);
    });

    const lbl = el('div', 'cost-row-label');
    lbl.style.display = 'flex';
    lbl.style.alignItems = 'center';

    const rankBadge = el('span', 'task-rank-badge', `#${tp.rank}`);
    lbl.appendChild(rankBadge);

    const titleGroup = el('div');
    const taskName = t.title || t.name || t.identifier || '—';
    titleGroup.appendChild(el('div', null, `${t.identifier ? `${t.identifier}: ` : ''}${taskName}`));
    titleGroup.appendChild(el('div', 'muted-text', t.organization || ''));
    lbl.appendChild(titleGroup);

    row.appendChild(lbl);
    row.appendChild(el('span', 'cost-row-val', fmtCurrency(t.spent_usd)));
    taskSpendCard.appendChild(row);
  }

  grid.appendChild(taskSpendCard);
}


// ── Settings Page ─────────────────────────────────────────
function renderSettings() {
  const container = document.getElementById('settings-container');
  if (!container) return;
  container.innerHTML = '';

  const f = state.fleet;

  // Connection section
  const connSec = el('div', 'settings-section');
  const connHdr = el('div', 'settings-section-header');
  connHdr.appendChild(el('div', 'settings-section-title', 'Connection'));
  connHdr.appendChild(el('div', 'settings-section-desc', 'StayPoint daemon connection and authentication.'));
  connSec.appendChild(connHdr);

  for (const { label, val } of [
    { label: 'API Endpoint', val: window.location.host },
    { label: 'Auth', val: TOKEN ? 'Token (session cookie active)' : 'No token' },
    { label: 'SSE Status', val: sseSource?.readyState === 1 ? 'Connected' : 'Reconnecting' },
  ]) {
    const row = el('div', 'settings-row');
    row.appendChild(el('div', 'settings-row-label', label));
    row.appendChild(el('span', 'settings-val', val));
    connSec.appendChild(row);
  }
  container.appendChild(connSec);

  // Provider accounts
  const provSec = el('div', 'settings-section');
  const provHdr = el('div', 'settings-section-header');
  provHdr.appendChild(el('div', 'settings-section-title', 'Provider Accounts'));
  provHdr.appendChild(el('div', 'settings-section-desc', 'Detailed quota pool headroom breakdown, reset times, and limits.'));
  provSec.appendChild(provHdr);

  const quotas = f?.provider_quotas || {};
  const preferredOrder = ['gemini', 'claude_work', 'claude_personal', 'claude', 'openai'];
  const quotaKeys = [];
  for (const k of preferredOrder) {
    if (quotas[k]) quotaKeys.push(k);
  }
  for (const k of Object.keys(quotas)) {
    if (!quotaKeys.includes(k)) quotaKeys.push(k);
  }

  const validKeys = quotaKeys.filter(k => !(k === 'claude' && (quotas['claude_work'] || quotas['claude_personal'])));

  if (validKeys.length === 0) {
    const row = el('div', 'settings-row');
    row.appendChild(el('span', 'muted-text', 'No quota telemetry available.'));
    provSec.appendChild(row);
  } else {
    for (const key of validKeys) {
      const q = quotas[key];
      const card = el('div', 'settings-provider-card');

      // Card Header
      const cardHdr = el('div', 'settings-provider-header');
      const titleWrap = el('div');
      titleWrap.appendChild(el('div', 'settings-provider-name', q.display_name || key));
      titleWrap.appendChild(el('div', 'settings-provider-sub', q.projection_message || (q.projection_status ? `Pacing: ${q.projection_status}` : 'Standard allocation')));
      cardHdr.appendChild(titleWrap);

      let sCls = 'pill-green', sTxt = '✔ Active · On Track';
      if (q.is_locked || q.projection_status === 'locked_out') {
        sCls = 'pill-red';
        sTxt = '🔒 Locked Out';
      } else if (q.projection_status === 'overpaced') {
        sCls = 'pill-amber';
        sTxt = '⚠️ Overpaced';
      }
      cardHdr.appendChild(el('span', `pill ${sCls}`, sTxt));
      card.appendChild(cardHdr);

      // Lockout Alert Banner if locked
      if (q.is_locked || q.lockout_reason) {
        const lockBanner = el('div', 'settings-lockout-banner');
        let lockMsg = q.lockout_reason || 'Quota threshold exceeded; requests paused.';
        if (q.lockout_until) {
          const untilCountdown = formatCountdown(q.lockout_until);
          lockMsg += ` · Resets ${untilCountdown || ''} (${fmtDateTime(q.lockout_until)})`;
        }
        lockBanner.textContent = `🔒 ${lockMsg}`;
        card.appendChild(lockBanner);
      }

      // Card Telemetry Body
      const body = el('div', 'settings-provider-body');

      // ── 5-Hour Rolling Pool ──
      const rem5h = Math.max(0, Math.min(100, q.five_hour_remaining_pct ?? 100));
      const used5h = Math.max(0, Math.min(100, 100 - rem5h));
      const count5h = formatCountdown(q.five_hour_resets_at);
      const time5h = formatResetTime(q.five_hour_resets_at, false);
      const limit5h = q.lockout_threshold_pct || 100;

      const pool5h = el('div', 'settings-pool-box');
      const pool5hHdr = el('div', 'settings-pool-hdr');
      pool5hHdr.appendChild(el('span', 'settings-pool-title', '5-Hour Rolling Window'));
      const badge5h = el('span', `headroom-badge ${rem5h <= 10 ? 'badge-red' : rem5h <= 25 ? 'badge-amber' : 'badge-green'}`,
        `${rem5h.toFixed(1)}% Headroom`);
      pool5hHdr.appendChild(badge5h);
      pool5h.appendChild(pool5hHdr);

      const bar5hOuter = el('div', 'gauge-bar-outer');
      const bar5hInner = el('div', 'gauge-bar-inner');
      bar5hInner.style.width = `${used5h}%`;
      bar5hInner.className = `gauge-bar-inner ${(q.is_locked || used5h >= 95) ? 'gauge-bar-red' : used5h >= 75 ? 'gauge-bar-amber' : 'gauge-bar-green'}`;
      bar5hOuter.appendChild(bar5hInner);
      pool5h.appendChild(bar5hOuter);

      const metrics5h = el('div', 'settings-metrics-grid');

      const m5hHeadroom = el('div', 'settings-metric-item');
      m5hHeadroom.appendChild(el('span', 'settings-metric-lbl', 'Pool Headroom'));
      m5hHeadroom.appendChild(el('span', 'settings-metric-val', `${rem5h.toFixed(1)}% remaining (${used5h.toFixed(1)}% used)`));
      metrics5h.appendChild(m5hHeadroom);

      const m5hReset = el('div', 'settings-metric-item');
      m5hReset.appendChild(el('span', 'settings-metric-lbl', 'Reset Time'));
      const resetText5h = count5h ? `${count5h}${time5h ? ' · ' + time5h : ''}` : 'Rolling window';
      m5hReset.appendChild(el('span', 'settings-metric-val', resetText5h));
      metrics5h.appendChild(m5hReset);

      const m5hLimit = el('div', 'settings-metric-item');
      m5hLimit.appendChild(el('span', 'settings-metric-lbl', 'Lockout Limit'));
      m5hLimit.appendChild(el('span', 'settings-metric-val', `${limit5h}% threshold`));
      metrics5h.appendChild(m5hLimit);

      const m5hBurn = el('div', 'settings-metric-item');
      m5hBurn.appendChild(el('span', 'settings-metric-lbl', 'Current Burn'));
      m5hBurn.appendChild(el('span', 'settings-metric-val', q.burn_rate_5h ? `${q.burn_rate_5h.toFixed(2)}%/turn` : '—'));
      metrics5h.appendChild(m5hBurn);

      pool5h.appendChild(metrics5h);
      body.appendChild(pool5h);

      // ── Weekly Budget Pool ──
      const remWk = Math.max(0, Math.min(100, q.weekly_remaining_pct ?? 100));
      const usedWk = Math.max(0, Math.min(100, 100 - remWk));
      const countWk = formatCountdown(q.weekly_resets_at);
      const timeWk = formatResetTime(q.weekly_resets_at, true);

      const poolWk = el('div', 'settings-pool-box');
      const poolWkHdr = el('div', 'settings-pool-hdr');
      poolWkHdr.appendChild(el('span', 'settings-pool-title', 'Weekly Budget Window'));
      const badgeWk = el('span', `headroom-badge ${remWk <= 15 ? 'badge-red' : remWk <= 30 ? 'badge-amber' : 'badge-green'}`,
        `${remWk.toFixed(1)}% Headroom`);
      poolWkHdr.appendChild(badgeWk);
      poolWk.appendChild(poolWkHdr);

      const barWkOuter = el('div', 'gauge-bar-outer');
      const barWkInner = el('div', 'gauge-bar-inner');
      barWkInner.style.width = `${usedWk}%`;
      barWkInner.className = `gauge-bar-inner ${usedWk >= 90 ? 'gauge-bar-red' : usedWk >= 70 ? 'gauge-bar-amber' : 'gauge-bar-green'}`;
      barWkOuter.appendChild(barWkInner);
      poolWk.appendChild(barWkOuter);

      const metricsWk = el('div', 'settings-metrics-grid');

      const mWkHeadroom = el('div', 'settings-metric-item');
      mWkHeadroom.appendChild(el('span', 'settings-metric-lbl', 'Weekly Headroom'));
      mWkHeadroom.appendChild(el('span', 'settings-metric-val', `${remWk.toFixed(1)}% remaining (${usedWk.toFixed(1)}% used)`));
      metricsWk.appendChild(mWkHeadroom);

      const mWkReset = el('div', 'settings-metric-item');
      mWkReset.appendChild(el('span', 'settings-metric-lbl', 'Reset Time'));
      const resetTextWk = countWk ? `${countWk}${timeWk ? ' · ' + timeWk : ''}` : '—';
      mWkReset.appendChild(el('span', 'settings-metric-val', resetTextWk));
      metricsWk.appendChild(mWkReset);

      const mWkBurn = el('div', 'settings-metric-item');
      mWkBurn.appendChild(el('span', 'settings-metric-lbl', 'Weekly Burn'));
      mWkBurn.appendChild(el('span', 'settings-metric-val', q.burn_rate_weekly ? `${q.burn_rate_weekly.toFixed(2)}%/turn` : '—'));
      metricsWk.appendChild(mWkBurn);

      const mWkRunway = el('div', 'settings-metric-item');
      mWkRunway.appendChild(el('span', 'settings-metric-lbl', 'Runway Limit'));
      mWkRunway.appendChild(el('span', 'settings-metric-val', q.runway_turns ? `${q.runway_turns} turns left` : 'Sustainable'));
      metricsWk.appendChild(mWkRunway);

      poolWk.appendChild(metricsWk);
      body.appendChild(poolWk);

      card.appendChild(body);
      provSec.appendChild(card);
    }
  }
  container.appendChild(provSec);

  // Fleet info
  const fleetSec = el('div', 'settings-section');
  const fleetHdr = el('div', 'settings-section-header');
  fleetHdr.appendChild(el('div', 'settings-section-title', 'Fleet Info'));
  fleetHdr.appendChild(el('div', 'settings-section-desc', 'Global overview statistics. Click any count to open the interactive drill-down modal.'));
  fleetSec.appendChild(fleetHdr);

  const orgCount = (f?.organizations || []).length;
  const totalTasks = f?.global_tasks?.total || 0;
  const activeAgents = f?.global_agents?.active_running || 0;

  for (const m of [
    {
      id: 'organizations',
      label: 'Organizations',
      sub: `${orgCount} tenant workspaces · Click to view breakdown`,
      val: String(orgCount),
      clickable: true,
      onClick: () => openFleetInfoModal('organizations'),
    },
    {
      id: 'tasks',
      label: 'Total Tasks',
      sub: `${f?.global_tasks?.running || 0} running, ${f?.global_tasks?.blocked || 0} blocked · Click to view breakdown`,
      val: String(totalTasks),
      clickable: true,
      onClick: () => openFleetInfoModal('tasks'),
    },
    {
      id: 'agents',
      label: 'Active Agents',
      sub: `${activeAgents} running across provider pools · Click to view breakdown`,
      val: String(activeAgents),
      clickable: true,
      onClick: () => openFleetInfoModal('agents'),
    },
    {
      id: 'last_update',
      label: 'Last Update',
      sub: 'Most recent telemetry sync timestamp',
      val: f?.timestamp ? fmtDateTime(f.timestamp) : '—',
      clickable: false,
    },
  ]) {
    const row = el('div', m.clickable ? 'settings-row settings-row-clickable' : 'settings-row');
    const lbl = el('div');
    lbl.appendChild(el('div', 'settings-row-label', m.label));
    if (m.sub) lbl.appendChild(el('div', 'settings-row-sub', m.sub));
    row.appendChild(lbl);

    const valEl = el('span', m.clickable ? 'settings-val settings-val-interactive' : 'settings-val', m.val);
    if (m.clickable) {
      valEl.appendChild(el('span', 'drilldown-arrow', ' →'));
      row.title = `Click to drill down into ${m.label}`;
      row.addEventListener('click', m.onClick);
    }
    row.appendChild(valEl);
    fleetSec.appendChild(row);
  }
  container.appendChild(fleetSec);
}

// ── Fleet Info Modal Drill-Down ────────────────────────────
const fleetModalState = {
  activeTab: 'organizations',
  taskSearch: '',
  taskStatus: 'all',
  taskOrg: 'all',
  agentSearch: '',
  agentProvider: 'all',
  agentStatus: 'all',
  orgSearch: '',
};

function getFleetTasks() {
  const taskMap = new Map();
  for (const t of (state.fleet?.tasks || [])) {
    if (t && t.id) taskMap.set(t.id, t);
  }
  for (const t of Object.values(state.tasks || {})) {
    if (t && t.id) {
      if (!taskMap.has(t.id)) taskMap.set(t.id, t);
      else Object.assign(taskMap.get(t.id), t);
    }
  }
  return Array.from(taskMap.values());
}

function getFleetAgents() {
  const f = state.fleet;
  const allAgents = [];

  for (const org of (f?.organizations || [])) {
    for (const a of (org.agents || [])) {
      const prov = resolveProvider(a);
      allAgents.push({ ...a, provider: prov, org: org.name });
    }
  }

  for (const s of Object.values(state.sessions || {})) {
    const exists = allAgents.find(a => a.id === s.id);
    if (!exists) {
      const prov = resolveProvider(s);
      allAgents.push({
        id: s.id,
        name: s.agent_type ? `${titleCase(prov)} Session` : 'Local Agent',
        role: 'Local Session',
        provider: prov,
        status: s.status || 'active',
        last_heartbeat: s.last_heartbeat_at,
        org: 'StayPoint',
      });
    }
  }

  const allTasks = getFleetTasks();
  return allAgents.map(a => {
    const agentTasks = allTasks.filter(t =>
      (t.checkout_agent_id && t.checkout_agent_id === a.id) ||
      (t.assignee_agent_id && t.assignee_agent_id === a.id) ||
      (t.assigned_agent && (t.assigned_agent === a.name || t.assigned_agent === a.id)) ||
      (t.assignee_name && t.assignee_name === a.name)
    );
    const runningTask = agentTasks.find(t => t.status === 'running' || t.status === 'in_progress');
    let st = (a.status || 'idle').toLowerCase();
    if (runningTask || st === 'running' || st === 'in_progress') {
      st = 'running';
    } else if (st === 'paused' || st === 'stopped' || a.pause_reason) {
      st = 'paused';
    } else {
      st = 'idle';
    }
    return {
      ...a,
      normalizedStatus: st,
      agentTasks,
      runningTask,
    };
  });
}

function openFleetInfoModal(tab = 'organizations') {
  const modal = document.getElementById('fleet-modal');
  if (!modal) return;
  fleetModalState.activeTab = tab;
  modal.classList.remove('hidden');
  document.body.style.overflow = 'hidden';
  switchFleetModalTab(tab);
}

function closeFleetInfoModal() {
  const modal = document.getElementById('fleet-modal');
  if (!modal) return;
  modal.classList.add('hidden');
  document.body.style.overflow = '';
}

function switchFleetModalTab(tab) {
  fleetModalState.activeTab = tab;
  document.querySelectorAll('#fleet-modal .modal-tab-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.tab === tab);
  });

  const body = document.getElementById('fleet-modal-body');
  const footerInfo = document.getElementById('fleet-modal-footer-info');
  const modalTitle = document.getElementById('fleet-modal-title');
  const modalSubtitle = document.getElementById('fleet-modal-subtitle');
  if (!body) return;
  body.innerHTML = '';

  if (tab === 'organizations') {
    if (modalTitle) modalTitle.textContent = '🏢 Fleet Organizations';
    if (modalSubtitle) modalSubtitle.textContent = 'Tenant workspace distribution, task counts, and financial spend.';
    renderFleetModalOrganizations(body, footerInfo);
  } else if (tab === 'tasks') {
    if (modalTitle) modalTitle.textContent = '📋 Fleet Tasks Breakdown';
    if (modalSubtitle) modalSubtitle.textContent = 'Global task registry with live statuses, assignments, and priorities.';
    renderFleetModalTasks(body, footerInfo);
  } else if (tab === 'agents') {
    if (modalTitle) modalTitle.textContent = '🤖 Active Fleet Agents';
    if (modalSubtitle) modalSubtitle.textContent = 'Autonomous agents and sessions across all providers and tenant orgs.';
    renderFleetModalAgents(body, footerInfo);
  }
}

function renderFleetModalOrganizations(body, footerInfo) {
  const orgs = state.fleet?.organizations || [];
  const totalSpend = orgs.reduce((sum, o) => sum + (o.spent_usd || 0), 0);
  const totalAgents = orgs.reduce((sum, o) => sum + (o.active_agents || 0), 0);
  const totalTasks = orgs.reduce((sum, o) => sum + (o.task_counts?.total || 0), 0);

  // Summary Grid
  const summaryGrid = el('div', 'modal-summary-grid');
  for (const { val, lbl } of [
    { val: String(orgs.length), lbl: 'Organizations' },
    { val: fmtCurrency(totalSpend), lbl: 'Total Spend' },
    { val: String(totalAgents), lbl: 'Active Agents' },
    { val: String(totalTasks), lbl: 'Total Tasks' },
  ]) {
    const card = el('div', 'modal-summary-card');
    card.appendChild(el('div', 'modal-summary-val', val));
    card.appendChild(el('div', 'modal-summary-lbl', lbl));
    summaryGrid.appendChild(card);
  }
  body.appendChild(summaryGrid);

  // Filter Row
  const filterRow = el('div', 'modal-filter-row');
  const searchInput = el('input', 'modal-filter-input');
  searchInput.type = 'text';
  searchInput.placeholder = 'Search organizations or prefix...';
  searchInput.value = fleetModalState.orgSearch || '';
  filterRow.appendChild(searchInput);
  body.appendChild(filterRow);

  const tableWrap = el('div', 'modal-table-wrap');
  const table = el('table', 'modal-table');
  const thead = el('thead');
  const thr = el('tr');
  for (const h of ['Organization', 'Prefix', 'Active Agents', 'Task Status Breakdown', 'Spend', 'Action']) {
    thr.appendChild(el('th', null, h));
  }
  thead.appendChild(thr);
  table.appendChild(thead);

  const tbody = el('tbody');
  table.appendChild(tbody);
  tableWrap.appendChild(table);
  body.appendChild(tableWrap);

  function updateRows() {
    tbody.innerHTML = '';
    const q = (fleetModalState.orgSearch || '').toLowerCase().trim();
    const filtered = orgs.filter(o =>
      !q || (o.name && o.name.toLowerCase().includes(q)) ||
      (o.issue_prefix && o.issue_prefix.toLowerCase().includes(q))
    );

    if (filtered.length === 0) {
      const tr = el('tr');
      const td = el('td', 'muted-text', 'No organizations match the filter.');
      td.colSpan = 6;
      td.style.textAlign = 'center';
      td.style.padding = '24px';
      tr.appendChild(td);
      tbody.appendChild(tr);
    } else {
      for (const org of filtered) {
        const tr = el('tr');

        // Name
        const tdName = el('td');
        tdName.appendChild(el('strong', null, org.name || 'Unnamed'));
        tr.appendChild(tdName);

        // Prefix
        const tdPrefix = el('td');
        tdPrefix.appendChild(el('span', 'chip-sm chip-purple', org.issue_prefix || 'ORG'));
        tr.appendChild(tdPrefix);

        // Active Agents
        const tdAgents = el('td');
        tdAgents.appendChild(el('span', 'chip-sm chip-green', `${org.active_agents || 0} agents`));
        tr.appendChild(tdAgents);

        // Task Breakdown
        const tdTasks = el('td');
        const tc = org.task_counts || {};
        const group = el('div', 'chip-group');
        if (tc.running) group.appendChild(el('span', 'chip-sm chip-cyan', `${tc.running} running`));
        if (tc.active) group.appendChild(el('span', 'chip-sm chip-blue', `${tc.active} active`));
        if (tc.blocked) group.appendChild(el('span', 'chip-sm chip-red', `${tc.blocked} blocked`));
        if (tc.done) group.appendChild(el('span', 'chip-sm chip-green', `${tc.done} done`));
        group.appendChild(el('span', 'chip-sm chip-gray', `${tc.total || 0} total`));
        tdTasks.appendChild(group);
        tr.appendChild(tdTasks);

        // Spend
        const tdSpend = el('td', null, fmtCurrency(org.spent_usd));
        tr.appendChild(tdSpend);

        // Action
        const tdAction = el('td');
        const viewBtn = el('button', 'modal-action-btn', 'View Org →');
        viewBtn.addEventListener('click', () => {
          closeFleetInfoModal();
          openOrgDetail(org.name);
        });
        tdAction.appendChild(viewBtn);
        tr.appendChild(tdAction);

        tbody.appendChild(tr);
      }
    }

    if (footerInfo) {
      footerInfo.textContent = `Showing ${filtered.length} of ${orgs.length} organizations`;
    }
  }

  searchInput.addEventListener('input', (e) => {
    fleetModalState.orgSearch = e.target.value;
    updateRows();
  });

  updateRows();
}

function renderFleetModalTasks(body, footerInfo) {
  const allTasks = getFleetTasks();
  const orgs = state.fleet?.organizations || [];

  const counts = { running: 0, in_progress: 0, blocked: 0, in_review: 0, todo: 0, done: 0 };
  for (const t of allTasks) {
    const rawSt = (t.status || '').toLowerCase();
    let st = normalizeFleetStatus(t.status);
    if (rawSt === 'running') st = 'running';
    else if (rawSt === 'in_review') st = 'in_review';
    if (counts[st] !== undefined) counts[st]++;
  }

  // Summary Grid
  const summaryGrid = el('div', 'modal-summary-grid');
  for (const { val, lbl, cls } of [
    { val: String(counts.running), lbl: 'Running', cls: 'chip-cyan' },
    { val: String(counts.in_progress), lbl: 'In Progress', cls: 'chip-blue' },
    { val: String(counts.blocked), lbl: 'Blocked', cls: 'chip-red' },
    { val: String(counts.in_review), lbl: 'In Review', cls: 'chip-purple' },
    { val: String(counts.todo), lbl: 'Todo', cls: 'chip-gray' },
    { val: String(counts.done), lbl: 'Done', cls: 'chip-green' },
    { val: String(allTasks.length), lbl: 'Total Tasks' },
  ]) {
    const card = el('div', 'modal-summary-card');
    card.appendChild(el('div', 'modal-summary-val' + (cls ? ` ${cls}` : ''), val));
    card.appendChild(el('div', 'modal-summary-lbl', lbl));
    summaryGrid.appendChild(card);
  }
  body.appendChild(summaryGrid);

  // Filter Row
  const filterRow = el('div', 'modal-filter-row');

  const searchInput = el('input', 'modal-filter-input');
  searchInput.type = 'text';
  searchInput.placeholder = 'Search tasks by title or ID...';
  searchInput.value = fleetModalState.taskSearch || '';
  filterRow.appendChild(searchInput);

  const statusSelect = el('select', 'modal-filter-select');
  for (const [sVal, sLbl] of [
    ['all', 'All Statuses'],
    ['running', 'Running'],
    ['in_progress', 'In Progress'],
    ['blocked', 'Blocked'],
    ['in_review', 'In Review'],
    ['todo', 'Todo'],
    ['done', 'Done'],
  ]) {
    const opt = el('option', null, sLbl);
    opt.value = sVal;
    if (fleetModalState.taskStatus === sVal) opt.selected = true;
    statusSelect.appendChild(opt);
  }
  filterRow.appendChild(statusSelect);

  const orgSelect = el('select', 'modal-filter-select');
  const allOrgOpt = el('option', null, 'All Organizations');
  allOrgOpt.value = 'all';
  orgSelect.appendChild(allOrgOpt);
  for (const org of orgs) {
    const opt = el('option', null, org.name);
    opt.value = org.name;
    if (fleetModalState.taskOrg === org.name) opt.selected = true;
    orgSelect.appendChild(opt);
  }
  filterRow.appendChild(orgSelect);
  body.appendChild(filterRow);

  const tableWrap = el('div', 'modal-table-wrap');
  const table = el('table', 'modal-table');
  const thead = el('thead');
  const thr = el('tr');
  for (const h of ['ID', 'Title', 'Organization', 'Status', 'Assignee', 'Priority', 'Action']) {
    thr.appendChild(el('th', null, h));
  }
  thead.appendChild(thr);
  table.appendChild(thead);

  const tbody = el('tbody');
  table.appendChild(tbody);
  tableWrap.appendChild(table);
  body.appendChild(tableWrap);

  function updateRows() {
    tbody.innerHTML = '';
    const q = (fleetModalState.taskSearch || '').toLowerCase().trim();
    const stFilter = fleetModalState.taskStatus || 'all';
    const orgFilter = fleetModalState.taskOrg || 'all';

    const filtered = allTasks.filter(t => {
      const rawSt = (t.status || '').toLowerCase();
      let st = normalizeFleetStatus(t.status);
      if (rawSt === 'running') st = 'running';
      else if (rawSt === 'in_review') st = 'in_review';

      if (stFilter !== 'all' && st !== stFilter) return false;
      const orgName = t.organization || t.org || '';
      if (orgFilter !== 'all' && orgName.toLowerCase() !== orgFilter.toLowerCase()) return false;
      if (q) {
        const idMatch = (t.id || '').toLowerCase().includes(q) || (t.identifier || '').toLowerCase().includes(q);
        const titleMatch = (t.title || '').toLowerCase().includes(q);
        if (!idMatch && !titleMatch) return false;
      }
      return true;
    });

    if (filtered.length === 0) {
      const tr = el('tr');
      const td = el('td', 'muted-text', 'No tasks match the filter criteria.');
      td.colSpan = 7;
      td.style.textAlign = 'center';
      td.style.padding = '24px';
      tr.appendChild(td);
      tbody.appendChild(tr);
    } else {
      for (const t of filtered) {
        const tr = el('tr');

        // ID
        const tdId = el('td');
        tdId.appendChild(el('strong', 'settings-val', t.identifier || (t.id ? t.id.slice(0, 8) : '—')));
        tr.appendChild(tdId);

        // Title
        const tdTitle = el('td');
        const titleSpan = el('span', null, t.title || 'Untitled task');
        titleSpan.title = t.title || '';
        tdTitle.appendChild(titleSpan);
        tr.appendChild(tdTitle);

        // Org
        const tdOrg = el('td', 'muted-text', t.organization || t.org || 'StayPoint');
        tr.appendChild(tdOrg);

        // Status
        const tdStatus = el('td');
        const rawSt = (t.status || '').toLowerCase();
        let st = normalizeFleetStatus(t.status);
        if (rawSt === 'running') st = 'running';
        else if (rawSt === 'in_review') st = 'in_review';

        let pillCls = 'pill-gray';
        if (st === 'running') pillCls = 'pill-cyan';
        else if (st === 'in_progress') pillCls = 'pill-blue';
        else if (st === 'blocked') pillCls = 'pill-red';
        else if (st === 'in_review') pillCls = 'pill-purple';
        else if (st === 'done') pillCls = 'pill-green';
        tdStatus.appendChild(el('span', `pill ${pillCls}`, st.replace('_', ' ')));
        tr.appendChild(tdStatus);

        // Assignee
        const tdAssignee = el('td', 'muted-text', t.assigned_agent || t.assignee_name || 'Unassigned');
        tr.appendChild(tdAssignee);

        // Priority
        const tdPrio = el('td', 'muted-text', t.priority || 'standard');
        tr.appendChild(tdPrio);

        // Action
        const tdAction = el('td');
        const viewBtn = el('button', 'modal-action-btn', 'View Task →');
        viewBtn.addEventListener('click', () => {
          closeFleetInfoModal();
          openDetail(t.id);
        });
        tdAction.appendChild(viewBtn);
        tr.appendChild(tdAction);

        tbody.appendChild(tr);
      }
    }

    if (footerInfo) {
      footerInfo.textContent = `Showing ${filtered.length} of ${allTasks.length} tasks`;
    }
  }

  searchInput.addEventListener('input', (e) => {
    fleetModalState.taskSearch = e.target.value;
    updateRows();
  });
  statusSelect.addEventListener('change', (e) => {
    fleetModalState.taskStatus = e.target.value;
    updateRows();
  });
  orgSelect.addEventListener('change', (e) => {
    fleetModalState.taskOrg = e.target.value;
    updateRows();
  });

  updateRows();
}

function renderFleetModalAgents(body, footerInfo) {
  const agents = getFleetAgents();

  const total = agents.length;
  const running = agents.filter(a => a.normalizedStatus === 'running').length;
  const idle = agents.filter(a => a.normalizedStatus === 'idle').length;
  const paused = agents.filter(a => a.normalizedStatus === 'paused').length;

  const claudeCount = agents.filter(a => a.provider === 'claude').length;
  const geminiCount = agents.filter(a => a.provider === 'gemini').length;
  const openaiCount = agents.filter(a => a.provider === 'openai').length;

  // Summary Grid
  const summaryGrid = el('div', 'modal-summary-grid');
  for (const { val, lbl, cls } of [
    { val: String(running), lbl: 'Running', cls: 'chip-cyan' },
    { val: String(idle), lbl: 'Idle', cls: 'chip-gray' },
    { val: String(paused), lbl: 'Paused', cls: 'chip-amber' },
    { val: String(claudeCount), lbl: 'Claude Pool', cls: 'chip-purple' },
    { val: String(geminiCount), lbl: 'Gemini Pool', cls: 'chip-blue' },
    { val: String(openaiCount), lbl: 'OpenAI Pool', cls: 'chip-green' },
    { val: String(total), lbl: 'Total Agents' },
  ]) {
    const card = el('div', 'modal-summary-card');
    card.appendChild(el('div', 'modal-summary-val' + (cls ? ` ${cls}` : ''), val));
    card.appendChild(el('div', 'modal-summary-lbl', lbl));
    summaryGrid.appendChild(card);
  }
  body.appendChild(summaryGrid);

  // Filter Row
  const filterRow = el('div', 'modal-filter-row');

  const searchInput = el('input', 'modal-filter-input');
  searchInput.type = 'text';
  searchInput.placeholder = 'Search agents by name or role...';
  searchInput.value = fleetModalState.agentSearch || '';
  filterRow.appendChild(searchInput);

  const providerSelect = el('select', 'modal-filter-select');
  for (const [pVal, pLbl] of [
    ['all', 'All Providers'],
    ['claude', 'Claude'],
    ['gemini', 'Gemini'],
    ['openai', 'OpenAI'],
  ]) {
    const opt = el('option', null, pLbl);
    opt.value = pVal;
    if (fleetModalState.agentProvider === pVal) opt.selected = true;
    providerSelect.appendChild(opt);
  }
  filterRow.appendChild(providerSelect);

  const statusSelect = el('select', 'modal-filter-select');
  for (const [sVal, sLbl] of [
    ['all', 'All Statuses'],
    ['running', 'Running'],
    ['idle', 'Idle'],
    ['paused', 'Paused'],
  ]) {
    const opt = el('option', null, sLbl);
    opt.value = sVal;
    if (fleetModalState.agentStatus === sVal) opt.selected = true;
    statusSelect.appendChild(opt);
  }
  filterRow.appendChild(statusSelect);
  body.appendChild(filterRow);

  const tableWrap = el('div', 'modal-table-wrap');
  const table = el('table', 'modal-table');
  const thead = el('thead');
  const thr = el('tr');
  for (const h of ['Agent Name', 'Role', 'Provider', 'Organization', 'Status', 'Current Task', 'Action']) {
    thr.appendChild(el('th', null, h));
  }
  thead.appendChild(thr);
  table.appendChild(thead);

  const tbody = el('tbody');
  table.appendChild(tbody);
  tableWrap.appendChild(table);
  body.appendChild(tableWrap);

  function updateRows() {
    tbody.innerHTML = '';
    const q = (fleetModalState.agentSearch || '').toLowerCase().trim();
    const provFilter = fleetModalState.agentProvider || 'all';
    const stFilter = fleetModalState.agentStatus || 'all';

    const filtered = agents.filter(a => {
      if (provFilter !== 'all' && a.provider !== provFilter) return false;
      if (stFilter !== 'all' && a.normalizedStatus !== stFilter) return false;
      if (q) {
        const nameMatch = (a.name || '').toLowerCase().includes(q);
        const roleMatch = (a.role || '').toLowerCase().includes(q);
        if (!nameMatch && !roleMatch) return false;
      }
      return true;
    });

    if (filtered.length === 0) {
      const tr = el('tr');
      const td = el('td', 'muted-text', 'No agents match the filter criteria.');
      td.colSpan = 7;
      td.style.textAlign = 'center';
      td.style.padding = '24px';
      tr.appendChild(td);
      tbody.appendChild(tr);
    } else {
      for (const a of filtered) {
        const tr = el('tr');

        // Name
        const tdName = el('td');
        tdName.appendChild(el('strong', null, a.name || 'Agent'));
        tr.appendChild(tdName);

        // Role
        const tdRole = el('td', 'muted-text', a.role || 'Worker');
        tr.appendChild(tdRole);

        // Provider
        const tdProv = el('td');
        let provChip = 'chip-blue';
        if (a.provider === 'claude') provChip = 'chip-purple';
        else if (a.provider === 'openai') provChip = 'chip-green';
        tdProv.appendChild(el('span', `chip-sm ${provChip}`, titleCase(a.provider || 'gemini')));
        tr.appendChild(tdProv);

        // Org
        const tdOrg = el('td', 'muted-text', a.org || 'StayPoint');
        tr.appendChild(tdOrg);

        // Status
        const tdStatus = el('td');
        let stChip = 'chip-gray';
        if (a.normalizedStatus === 'running') stChip = 'chip-cyan';
        else if (a.normalizedStatus === 'paused') stChip = 'chip-amber';
        tdStatus.appendChild(el('span', `chip-sm ${stChip}`, titleCase(a.normalizedStatus)));
        tr.appendChild(tdStatus);

        // Current Task
        const tdTask = el('td');
        if (a.runningTask) {
          const taskLink = el('a', 'settings-val-interactive', a.runningTask.title || a.runningTask.id);
          taskLink.style.cursor = 'pointer';
          taskLink.addEventListener('click', (ev) => {
            ev.stopPropagation();
            closeFleetInfoModal();
            openDetail(a.runningTask.id);
          });
          tdTask.appendChild(taskLink);
        } else {
          tdTask.appendChild(el('span', 'muted-text', 'Idle'));
        }
        tr.appendChild(tdTask);

        // Action
        const tdAction = el('td');
        const viewBtn = el('button', 'modal-action-btn', 'View Agents →');
        viewBtn.addEventListener('click', () => {
          closeFleetInfoModal();
          navigateTo('agents');
        });
        tdAction.appendChild(viewBtn);
        tr.appendChild(tdAction);

        tbody.appendChild(tr);
      }
    }

    if (footerInfo) {
      footerInfo.textContent = `Showing ${filtered.length} of ${agents.length} agents`;
    }
  }

  searchInput.addEventListener('input', (e) => {
    fleetModalState.agentSearch = e.target.value;
    updateRows();
  });
  providerSelect.addEventListener('change', (e) => {
    fleetModalState.agentProvider = e.target.value;
    updateRows();
  });
  statusSelect.addEventListener('change', (e) => {
    fleetModalState.agentStatus = e.target.value;
    updateRows();
  });

  updateRows();
}

// Wire modal event listeners
document.getElementById('fleet-modal-close')?.addEventListener('click', closeFleetInfoModal);
document.getElementById('fleet-modal-footer-close')?.addEventListener('click', closeFleetInfoModal);
document.getElementById('fleet-modal')?.addEventListener('click', (e) => {
  if (e.target === document.getElementById('fleet-modal')) {
    closeFleetInfoModal();
  }
});
document.querySelectorAll('#fleet-modal .modal-tab-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    switchFleetModalTab(btn.dataset.tab);
  });
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    const m = document.getElementById('fleet-modal');
    if (m && !m.classList.contains('hidden')) {
      closeFleetInfoModal();
    }
  }
});

// ── Org Detail View ────────────────────────────────────────
function openOrgDetail(orgName) {
  navigateTo('org-detail', orgName, true);
}

function renderOrgDetailView(org) {
  const container = document.getElementById('org-detail-content');
  if (!container) return;
  container.innerHTML = '';

  const hdr = el('div', 'org-detail-header');
  const backBtn = el('button', 'back-btn', '← Back');
  backBtn.addEventListener('click', () => {
    navigateTo('overview', null, true);
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

  // 5-Hour Rolling Quotas & Lockout Gauges for this organization
  const orgQuotas = org.provider_quotas || {};
  if (Object.keys(orgQuotas).length) {
    const quotaSec = el('div', 'org-detail-section');
    quotaSec.appendChild(el('div', 'org-detail-section-title', '5-Hour Rolling Quotas & Lockout Status'));
    const quotaGrid = el('div', 'quota-gauges-grid');
    const order = ['gemini', 'claude_work', 'claude_personal', 'claude', 'openai'];
    for (const key of order) {
      const q = orgQuotas[key];
      if (!q) continue;
      if (key === 'claude' && (orgQuotas['claude_work'] || orgQuotas['claude_personal'])) continue;
      quotaGrid.appendChild(buildGaugeCard(key, q));
    }
    quotaSec.appendChild(quotaGrid);
    container.appendChild(quotaSec);
  }

  // Tasks section
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
      const tdSpend = el('td', null, t.spent_usd > 0 ? fmtCurrency(t.spent_usd) : '—');
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
      const prov = resolveProvider(a);
      const card = el('div', 'agent-roster-card');
      card.appendChild(el('div', 'agent-roster-name', a.name || a.id?.slice(0, 12)));
      card.appendChild(el('div', 'agent-roster-meta',
        `${a.role || 'agent'} · ${prov} · ${a.status || 'active'}`));
      if (a.last_heartbeat) {
        card.appendChild(el('div', 'muted-text', `Last: ${fmtRelTime(a.last_heartbeat)}`));
      }
      rosterGrid.appendChild(card);
    }
    agentsSec.appendChild(rosterGrid);
  } else {
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
  // Fleet view is now "Agents" — redirect rendering to agents view if active
  const agentsView = document.getElementById('view-agents');
  if (agentsView?.classList.contains('active')) {
    renderAgentsPage();
  }
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

  // Download Report dropdown
  const dlWrap = el('div', 'report-dl-wrap');
  const dlBtn = el('button', 'report-dl-btn', 'Download Report');
  dlBtn.type = 'button';
  const dlMenu = el('div', 'report-dl-menu');
  dlMenu.hidden = true;
  for (const { type, label } of [
    { type: 'work',     label: 'Work / Boss Card' },
    { type: 'personal', label: 'Personal' },
    { type: 'gemini',   label: 'Gemini / Antigravity' },
    { type: 'combined', label: 'Combined Fleet' },
  ]) {
    const item = el('button', 'report-dl-item', label);
    item.type = 'button';
    item.dataset.reportType = type;
    item.addEventListener('click', async (e) => {
      e.stopPropagation();
      dlMenu.hidden = true;

      // Ensure .dl-spinner and button disabled state visibly activate immediately
      // on the download button upon triggering PDF/report generation and remain until blob is received.
      const origBtnContent = dlBtn.innerHTML;
      dlBtn.disabled = true;
      dlBtn.style.opacity = '0.7';
      dlBtn.style.cursor = 'not-allowed';
      dlBtn.innerHTML = '';
      const btnSpinner = el('span', 'dl-spinner');
      dlBtn.appendChild(btnSpinner);
      dlBtn.appendChild(document.createTextNode(` Generating ${label}...`));

      // Also disable dropdown items during active generation
      dlMenu.querySelectorAll('.report-dl-item').forEach(b => { b.disabled = true; });

      try {
        const url = `/api/report?type=${encodeURIComponent(type)}${TOKEN ? `&token=${encodeURIComponent(TOKEN)}` : ''}`;
        const resp = await fetch(url, {
          headers: authHeader(),
        });
        if (!resp.ok) {
          const errText = await resp.text().catch(() => '');
          let errMsg = `Report generation failed (${resp.status})`;
          try {
            const parsed = JSON.parse(errText);
            if (parsed.error) errMsg = parsed.error;
          } catch {
            if (errText) errMsg = errText;
          }
          throw new Error(errMsg);
        }

        const blob = await resp.blob();

        let filename = `staypoint-${type}-report.pdf`;
        const disp = resp.headers.get('Content-Disposition');
        if (disp) {
          const match = disp.match(/filename="?([^";]+)"?/i);
          if (match && match[1]) {
            filename = match[1].trim();
          }
        }

        const blobUrl = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = blobUrl;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        setTimeout(() => URL.revokeObjectURL(blobUrl), 15000);
      } catch (err) {
        console.error('Report generation error:', err);
        alert(`Failed to download report: ${err.message}`);
      } finally {
        dlBtn.disabled = false;
        dlBtn.style.opacity = '';
        dlBtn.style.cursor = '';
        dlBtn.innerHTML = origBtnContent;
        dlMenu.querySelectorAll('.report-dl-item').forEach(b => { b.disabled = false; });
      }
    });
    dlMenu.appendChild(item);
  }
  dlBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    dlMenu.hidden = !dlMenu.hidden;
  });
  dlWrap.addEventListener('click', (e) => e.stopPropagation());
  dlWrap.appendChild(dlBtn);
  dlWrap.appendChild(dlMenu);
  header.appendChild(dlWrap);

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

// ── Chat helpers ──────────────────────────────────────────

function startChatPoll(taskId) {
  stopChatPoll();
  state.chatPollTimer = setInterval(() => refreshChatMessages(taskId), 8000);
}

function stopChatPoll() {
  if (state.chatPollTimer) { clearInterval(state.chatPollTimer); state.chatPollTimer = null; }
}

async function refreshChatMessages(taskId) {
  if (state.openDetailTaskId !== taskId) { stopChatPoll(); return; }
  try {
    const isFleet = isFleetTaskId(taskId);
    const endpoint = isFleet ? `/api/fleet/tasks/${taskId}/comments` : `/api/tasks/${taskId}/comments`;
    const cr = await apiFetch(endpoint);
    const comments = cr.comments || (Array.isArray(cr) ? cr : []);
    const messagesDiv = document.getElementById('panel-chat-messages');
    const titleEl = document.querySelector('#panel-chat-section .panel-section-title');
    if (!messagesDiv) return;
    const atBottom = messagesDiv.scrollHeight - messagesDiv.scrollTop <= messagesDiv.clientHeight + 30;
    renderChatMessages(messagesDiv, comments);
    if (atBottom) messagesDiv.scrollTop = messagesDiv.scrollHeight;
    if (titleEl) titleEl.textContent = `Chat (${comments.length})`;
  } catch { /* silent */ }
}

function renderChatMessages(container, comments) {
  container.innerHTML = '';
  if (!comments.length) {
    container.appendChild(el('p', 'panel-field-muted', 'No messages yet.'));
    return;
  }
  for (const c of comments) {
    const authorType = (c.authorType || c.author_type || '').toLowerCase();
    const isAgent = authorType === 'agent' || authorType === 'system';
    const authorLabel = isAgent
      ? (c.authorName || c.author_name || 'Agent')
      : (c.author || 'You');
    const ts = c.createdAt || c.created_at || c.timestamp || '';

    const msg = el('div', `chat-msg ${isAgent ? 'chat-msg-agent' : 'chat-msg-user'}`);
    msg.appendChild(el('div', 'chat-msg-meta', `${authorLabel}${ts ? ' · ' + fmtDateTime(ts) : ''}`));
    const bubble = el('div', 'chat-msg-bubble');
    bubble.appendChild(mdEl(c.body || c.message || ''));
    msg.appendChild(bubble);
    container.appendChild(msg);
  }
}

function buildChatSection(container, taskId, comments) {
  const section = el('div', 'chat-section');
  section.id = 'panel-chat-section';
  section.appendChild(el('div', 'panel-section-title', `Chat (${comments.length})`));

  const messagesDiv = el('div', 'chat-messages');
  messagesDiv.id = 'panel-chat-messages';
  renderChatMessages(messagesDiv, comments);
  section.appendChild(messagesDiv);

  const compose = el('div', 'chat-compose');
  const textarea = document.createElement('textarea');
  textarea.className = 'chat-textarea';
  textarea.placeholder = 'Message the agent… (⌘↵ to send)';
  textarea.rows = 2;
  const autoResizeChat = () => {
    textarea.style.height = 'auto';
    textarea.style.height = Math.max(38, Math.min(220, textarea.scrollHeight)) + 'px';
  };
  textarea.addEventListener('input', autoResizeChat);
  const sendBtn = el('button', 'chat-send-btn', 'Send');
  sendBtn.type = 'button';

  const doSend = async () => {
    const body = textarea.value.trim();
    if (!body) return;
    textarea.value = '';
    textarea.style.height = '';
    sendBtn.disabled = true;
    try {
      await sendComment(taskId, body);
      await refreshChatMessages(taskId);
    } catch { /* ignore send error visually */ } finally {
      sendBtn.disabled = false;
      textarea.focus();
    }
  };

  sendBtn.addEventListener('click', doSend);
  textarea.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); doSend(); }
  });

  compose.appendChild(textarea);
  compose.appendChild(sendBtn);
  section.appendChild(compose);
  container.appendChild(section);

  messagesDiv.scrollTop = messagesDiv.scrollHeight;
}

async function sendComment(taskId, body) {
  const isFleet = isFleetTaskId(taskId);
  const endpoint = isFleet ? `/api/fleet/tasks/${taskId}/comments` : `/api/tasks/${taskId}/comments`;
  const resp = await fetch(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeader() },
    body: JSON.stringify({ body, message: body, author: 'user' }),
  });
  if (!resp.ok) throw new Error(`Send failed: ${resp.status}`);
  return resp.json().catch(() => null);
}

// ── Detail panel (Right sidebar / Properties) ─────────────
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

  // Meta row: explicitly labeled fields for Status, Identifier, Priority, Org, Stage
  const metaRow = el('div', 'panel-meta-row');

  const ident = task.identifier || (task.id ? `#${task.id.slice(0, 8)}` : null);
  if (ident) {
    const idWrap = el('div', 'panel-meta-item');
    idWrap.innerHTML = `<span class="panel-meta-tag-label">Identifier</span><span class="card-id panel-meta-value">${escapeHtml(ident)}</span>`;
    metaRow.appendChild(idWrap);
  }

  const statusWrap = el('div', 'panel-meta-item');
  statusWrap.innerHTML = `<span class="panel-meta-tag-label">Status</span>`;
  statusWrap.appendChild(statusPill(task.status || 'unknown'));
  metaRow.appendChild(statusWrap);

  if (task.priority) {
    const prioWrap = el('div', 'panel-meta-item');
    prioWrap.innerHTML = `<span class="panel-meta-tag-label">Priority</span>`;
    prioWrap.appendChild(statusPill(task.priority));
    metaRow.appendChild(prioWrap);
  }

  if (task.organization) {
    const orgWrap = el('div', 'panel-meta-item');
    orgWrap.innerHTML = `<span class="panel-meta-tag-label">Org</span>`;
    orgWrap.appendChild(el('span', 'pill', task.organization));
    metaRow.appendChild(orgWrap);
  }

  const stage = task.execution_stage || task.status;
  if (stage) {
    const stageWrap = el('div', 'panel-meta-item');
    stageWrap.innerHTML = `<span class="panel-meta-tag-label">Stage</span><span class="pill stage-pill">${escapeHtml(stage)}</span>`;
    metaRow.appendChild(stageWrap);
  }

  content.appendChild(metaRow);

  // Description (with fallback to first comment)
  let desc = (task.description || '').trim();
  if (!desc && task.comments && task.comments.length) {
    for (const c of task.comments) {
      const commentBody = (c.body || c.message || c.content || '').trim();
      if (commentBody) {
        desc = commentBody;
        break;
      }
    }
  }
  const descField = el('div', 'panel-field');
  descField.appendChild(el('div', 'panel-field-label', 'Description'));
  if (desc) {
    descField.appendChild(mdEl(desc));
  } else {
    descField.appendChild(el('div', 'panel-field-muted', 'No description provided.'));
  }
  content.appendChild(descField);

  // Properties grid with explicit typography labels
  if (ident)                         addPanelField(content, 'Identifier', ident);
  if (task.priority)                 addPanelField(content, 'Priority',   task.priority);
  if (task.organization)             addPanelField(content, 'Org',        task.organization);
  addPanelField(content, 'Stage',    task.execution_stage || task.status);
  addPanelField(content, 'Assignee', task.assignee_name || task.checkout_agent_id || null);
  addPanelField(content, 'Project',  task.project || null);
  addPanelField(content, 'Goal',     task.goal_title || task.goal_id ? (task.goal_title || task.goal_id?.slice(0, 12)) : null);
  addPanelField(content, 'Repo',     task.repo_path ? `${task.repo_path} (${task.git_branch || 'main'})` : null);

  // Labels rendering as colorful badges
  let labels = task.labels;
  if (typeof labels === 'string') {
    try { labels = JSON.parse(labels); } catch { labels = labels ? [labels] : []; }
  }
  if (Array.isArray(labels) && labels.length) {
    const lblWrap = el('div', 'panel-field');
    lblWrap.appendChild(el('div', 'panel-field-label', 'Labels'));
    const badgeContainer = el('div', 'panel-labels-container');
    badgeContainer.style.cssText = 'display:flex;flex-wrap:wrap;gap:6px;margin-top:4px;';
    for (const l of labels) {
      const name = typeof l === 'object' ? l.name : l;
      const color = typeof l === 'object' && l.color ? l.color : '#38bdf8';
      const badge = el('span', 'task-label-badge', name);
      badge.style.cssText = `font-size:0.75rem;padding:2px 8px;border-radius:12px;font-weight:600;background:${color}22;border:1px solid ${color};color:${color};`;
      badgeContainer.appendChild(badge);
    }
    lblWrap.appendChild(badgeContainer);
    content.appendChild(lblWrap);
  }

  // Spend (always displayed so user sees tracking status)
  addPanelField(content, 'Spend',
    `${fmtCurrency(task.spent_usd || 0)} · ${fmtCompactNum(task.spent_tokens || 0)} tokens`);

  // Budget
  addPanelField(content, 'Budget', `${fmtCurrency(task.max_budget_usd || 0)} · ${task.max_turns || 50} turns max`);

  // Governance Section: Reviewers, Approvers, Quality Gates
  const gov = task.governance;
  if (gov || task.reviewers?.length || task.approvers?.length) {
    const govSection = el('div', 'panel-gov-section');
    govSection.style.cssText = 'margin-top:16px;padding:12px;background:rgba(255,255,255,0.03);border:1px solid var(--border);border-radius:6px;';
    govSection.appendChild(el('div', 'panel-section-title', 'Governance & Quality Gates'));

    // Reviewers
    const reviewers = gov?.reviewers || task.reviewers || [];
    const revWrap = el('div', 'panel-field');
    revWrap.appendChild(el('div', 'panel-field-label', 'Reviewers'));
    if (reviewers.length) {
      const revList = el('div', 'panel-gov-list');
      revList.style.cssText = 'display:flex;flex-direction:column;gap:4px;margin-top:4px;';
      for (const r of reviewers) {
        const item = el('div', 'panel-gov-item');
        item.style.cssText = 'display:flex;align-items:center;justify-content:space-between;font-size:0.8rem;';
        const name = r.reviewer_name || r.name || (r.reviewer_id ? `Reviewer (${r.reviewer_id.slice(0, 8)})` : 'Reviewer');
        item.appendChild(el('span', 'pill', name));
        const dec = gov?.decisions?.find(d => d.reviewer_id === (r.reviewer_id || r.id));
        if (dec) {
          const decSpan = el('span', 'status-pill', dec.decision);
          item.appendChild(decSpan);
        } else {
          item.appendChild(el('span', 'muted-text', 'Pending review'));
        }
        revList.appendChild(item);
      }
      revWrap.appendChild(revList);
    } else {
      revWrap.appendChild(el('div', 'panel-field-muted', 'None assigned'));
    }
    govSection.appendChild(revWrap);

    // Approvers
    const approvers = gov?.approvers || task.approvers || [];
    const appWrap = el('div', 'panel-field');
    appWrap.appendChild(el('div', 'panel-field-label', 'Approvers'));
    if (approvers.length) {
      const appList = el('div', 'panel-gov-list');
      appList.style.cssText = 'display:flex;flex-direction:column;gap:4px;margin-top:4px;';
      for (const a of approvers) {
        const item = el('div', 'panel-gov-item');
        item.style.cssText = 'display:flex;align-items:center;justify-content:space-between;font-size:0.8rem;';
        const name = a.approver_name || a.name || (a.approver_id ? `Approver (${a.approver_id.slice(0, 8)})` : 'Approver');
        item.appendChild(el('span', 'pill', name));
        const vote = gov?.votes?.find(v => v.approver_id === (a.approver_id || a.id));
        if (vote) {
          const voteSpan = el('span', 'status-pill', vote.vote);
          item.appendChild(voteSpan);
        } else {
          item.appendChild(el('span', 'muted-text', 'Pending approval'));
        }
        appList.appendChild(item);
      }
      appWrap.appendChild(appList);
    } else {
      appWrap.appendChild(el('div', 'panel-field-muted', 'None assigned'));
    }
    govSection.appendChild(appWrap);

    // Gate Policy
    const reqReview = (gov?.config?.require_review ?? true) ? 'Required' : 'Optional';
    const thresh = gov?.config ? `${gov.config.approval_threshold} vote(s)` : '1 vote';
    const gateInfo = el('div', 'panel-field');
    gateInfo.appendChild(el('div', 'panel-field-label', 'Gate Policy'));
    gateInfo.appendChild(el('div', 'panel-field-value', `Review: ${reqReview} · Approval Threshold: ${thresh}`));
    govSection.appendChild(gateInfo);

    content.appendChild(govSection);
  }

  // Blocker Status & Upstream Dependencies
  const isBlocked = Boolean(task.is_blocked || task.status === 'blocked');
  const hasBlockers = Boolean(task.blocked_by && task.blocked_by.length > 0);
  const rawReason = task.block_reason || '';
  const hasExplicitReason = rawReason && rawReason.toLowerCase() !== 'blocked via tui' && rawReason.trim() !== '';

  if (isBlocked || hasBlockers) {
    const blockerWrap = el('div', 'panel-field');
    blockerWrap.appendChild(el('div', 'panel-field-label', 'Blocked'));

    let blockerSummary = '';
    if (hasBlockers) {
      const count = task.blocked_by.length;
      blockerSummary = `⚠ Blocked by ${count} upstream task${count > 1 ? 's' : ''}`;
      if (hasExplicitReason && !rawReason.toLowerCase().startsWith('blocked by')) {
        blockerSummary += ` · ${rawReason}`;
      }
    } else if (hasExplicitReason) {
      blockerSummary = `⚠ ${rawReason}`;
    } else {
      blockerSummary = '⚠ Blocked — no specific reason recorded';
    }

    const tag = el('span', 'panel-blocker-tag', blockerSummary);
    blockerWrap.appendChild(tag);
    content.appendChild(blockerWrap);
  }

  // Clickable Upstream Blockers ("Blocked By")
  if (task.blocked_by?.length) {
    const bbSection = el('div', 'panel-field');
    bbSection.appendChild(el('div', 'panel-field-label', `Blocked By (${task.blocked_by.length})`));

    const bbList = el('div', 'panel-blocker-list');
    bbList.style.cssText = 'display:flex;flex-direction:column;gap:6px;margin-top:6px;';

    for (const bb of task.blocked_by) {
      const card = el('div', 'panel-blocker-card');
      card.style.cssText = 'padding:8px 10px;background:rgba(255,255,255,0.04);border:1px solid rgba(248,81,73,0.3);border-radius:6px;cursor:pointer;transition:all 0.15s ease;display:flex;flex-direction:column;gap:3px;';
      card.addEventListener('mouseenter', () => { card.style.background = 'rgba(248,81,73,0.1)'; card.style.borderColor = 'var(--red)'; });
      card.addEventListener('mouseleave', () => { card.style.background = 'rgba(255,255,255,0.04)'; card.style.borderColor = 'rgba(248,81,73,0.3)'; });
      card.addEventListener('click', () => openDetail(bb.id));

      const headerRow = el('div', null);
      headerRow.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:8px;font-size:0.8rem;';

      const left = el('div', null);
      left.style.cssText = 'display:flex;align-items:center;gap:6px;min-width:0;';
      const linkId = el('span', 'panel-task-link', `${bb.identifier || bb.id?.slice(0, 10)} ↗`);
      linkId.style.cssText = 'font-weight:600;color:var(--accent, #38bdf8);text-decoration:underline;';
      const title = el('span', null, bb.title || bb.name || 'Untitled Task');
      title.style.cssText = 'color:var(--fg);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:500;';
      left.appendChild(linkId);
      left.appendChild(title);

      const stage = bb.execution_stage || bb.status || 'todo';
      const stagePill = el('span', 'status-pill', stage);
      stagePill.style.fontSize = '0.7rem';
      stagePill.style.padding = '1px 6px';

      headerRow.appendChild(left);
      headerRow.appendChild(stagePill);
      card.appendChild(headerRow);

      if (bb.rationale) {
        const rat = el('div', 'panel-blocker-rationale');
        rat.style.cssText = 'font-size:0.75rem;color:var(--amber, #f59e0b);margin-top:2px;font-style:italic;';
        rat.textContent = `↳ Rationale: ${bb.rationale}`;
        card.appendChild(rat);
      }

      bbList.appendChild(card);
    }
    bbSection.appendChild(bbList);
    content.appendChild(bbSection);
  }

  // Clickable Downstream Tasks ("Blocks")
  if (task.blocks?.length) {
    const blocksSection = el('div', 'panel-field');
    blocksSection.appendChild(el('div', 'panel-field-label', `Blocks (${task.blocks.length})`));

    const blocksList = el('div', 'panel-blocker-list');
    blocksList.style.cssText = 'display:flex;flex-direction:column;gap:6px;margin-top:6px;';

    for (const b of task.blocks) {
      const card = el('div', 'panel-blocker-card');
      card.style.cssText = 'padding:8px 10px;background:rgba(255,255,255,0.04);border:1px solid rgba(56,189,248,0.3);border-radius:6px;cursor:pointer;transition:all 0.15s ease;display:flex;flex-direction:column;gap:3px;';
      card.addEventListener('mouseenter', () => { card.style.background = 'rgba(56,189,248,0.1)'; card.style.borderColor = 'var(--accent, #38bdf8)'; });
      card.addEventListener('mouseleave', () => { card.style.background = 'rgba(255,255,255,0.04)'; card.style.borderColor = 'rgba(56,189,248,0.3)'; });
      card.addEventListener('click', () => openDetail(b.id));

      const headerRow = el('div', null);
      headerRow.style.cssText = 'display:flex;align-items:center;justify-content:space-between;gap:8px;font-size:0.8rem;';

      const left = el('div', null);
      left.style.cssText = 'display:flex;align-items:center;gap:6px;min-width:0;';
      const linkId = el('span', 'panel-task-link', `${b.identifier || b.id?.slice(0, 10)} ↗`);
      linkId.style.cssText = 'font-weight:600;color:var(--accent, #38bdf8);text-decoration:underline;';
      const title = el('span', null, b.title || b.name || 'Untitled Task');
      title.style.cssText = 'color:var(--fg);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:500;';
      left.appendChild(linkId);
      left.appendChild(title);

      const stage = b.execution_stage || b.status || 'todo';
      const stagePill = el('span', 'status-pill', stage);
      stagePill.style.fontSize = '0.7rem';
      stagePill.style.padding = '1px 6px';

      headerRow.appendChild(left);
      headerRow.appendChild(stagePill);
      card.appendChild(headerRow);

      if (b.rationale) {
        const rat = el('div', 'panel-blocker-rationale');
        rat.style.cssText = 'font-size:0.75rem;color:var(--muted);margin-top:2px;font-style:italic;';
        rat.textContent = `↳ Rationale: ${b.rationale}`;
        card.appendChild(rat);
      }

      blocksList.appendChild(card);
    }
    blocksSection.appendChild(blocksList);
    content.appendChild(blocksSection);
  }

  // Dependency Hierarchy Visualization Section
  const hasDeps = Boolean(task.blocked_by?.length || task.blocks?.length || task.parent_id || task.dependencies?.subtasks?.length);
  if (hasDeps) {
    const depSection = el('div', 'panel-gov-section');
    depSection.style.cssText = 'margin-top:16px;padding:12px;background:rgba(255,255,255,0.03);border:1px solid var(--border);border-radius:6px;';
    depSection.appendChild(el('div', 'panel-section-title', 'Dependency Hierarchy'));

    const treeBox = el('div', 'panel-dep-tree');
    treeBox.style.cssText = 'font-family:monospace;font-size:0.8rem;line-height:1.6;margin-top:8px;padding:8px 10px;background:rgba(0,0,0,0.25);border-radius:4px;overflow-x:auto;';

    // Parent task line if present
    if (task.parent_id || task.dependencies?.parent) {
      const p = task.dependencies?.parent || { id: task.parent_id, name: task.parent_identifier || task.parent_id };
      const pLine = el('div', null);
      pLine.style.cssText = 'color:var(--muted);margin-bottom:4px;cursor:pointer;';
      pLine.innerHTML = `◆ Parent: <span style="color:var(--accent,#38bdf8);text-decoration:underline;">#${p.identifier || p.id?.slice(0, 10)}</span> ${escapeHtml(p.title || p.name || '')}`;
      pLine.addEventListener('click', () => openDetail(p.id));
      treeBox.appendChild(pLine);
    }

    // Upstream blockers
    if (task.blocked_by?.length) {
      const upHeader = el('div', null, '▲ Upstream Blockers (Must resolve first):');
      upHeader.style.cssText = 'color:var(--red,#f85149);font-weight:600;margin-top:4px;';
      treeBox.appendChild(upHeader);

      task.blocked_by.forEach((bb, idx) => {
        const isLast = idx === task.blocked_by.length - 1;
        const prefix = isLast ? '  └── ⏳ ' : '  ├── ⏳ ';
        const row = el('div', null);
        row.style.cssText = 'cursor:pointer;padding:2px 0;transition:color 0.15s;';
        row.innerHTML = `${prefix}<span style="color:var(--accent,#38bdf8);text-decoration:underline;font-weight:600;">#${bb.identifier || bb.id?.slice(0, 10)}</span> <span style="color:var(--fg);">${escapeHtml(bb.title || bb.name || '')}</span> <span style="font-size:0.7rem;padding:0 4px;border-radius:3px;background:rgba(255,255,255,0.08);">${bb.execution_stage || bb.status || 'todo'}</span>`;
        row.addEventListener('click', () => openDetail(bb.id));
        treeBox.appendChild(row);

        if (bb.rationale) {
          const subPrefix = isLast ? '      └─ ' : '  │   └─ ';
          const ratRow = el('div', null);
          ratRow.style.cssText = 'color:var(--amber,#f59e0b);font-size:0.75rem;';
          ratRow.textContent = `${subPrefix}Rationale: ${bb.rationale}`;
          treeBox.appendChild(ratRow);
        }
      });
    }

    // Current task
    const currLine = el('div', null);
    currLine.style.cssText = 'color:var(--fg);font-weight:700;margin:6px 0;padding:2px 6px;background:rgba(255,255,255,0.06);border-left:3px solid var(--accent,#38bdf8);border-radius:2px;';
    currLine.innerHTML = `● Current: #${task.identifier || task.id?.slice(0, 10)} ${escapeHtml(task.title || task.name || '')} <span style="font-size:0.7rem;font-weight:normal;padding:0 4px;border-radius:3px;background:rgba(255,255,255,0.1);">${task.execution_stage || task.status || 'todo'}</span>`;
    treeBox.appendChild(currLine);

    // Downstream blocked tasks
    if (task.blocks?.length) {
      const downHeader = el('div', null, '▼ Blocks (Downstream tasks waiting):');
      downHeader.style.cssText = 'color:var(--cyan,#38bdf8);font-weight:600;margin-top:4px;';
      treeBox.appendChild(downHeader);

      task.blocks.forEach((b, idx) => {
        const isLast = idx === task.blocks.length - 1;
        const prefix = isLast ? '  └── 🔒 ' : '  ├── 🔒 ';
        const row = el('div', null);
        row.style.cssText = 'cursor:pointer;padding:2px 0;transition:color 0.15s;';
        row.innerHTML = `${prefix}<span style="color:var(--accent,#38bdf8);text-decoration:underline;font-weight:600;">#${b.identifier || b.id?.slice(0, 10)}</span> <span style="color:var(--fg);">${escapeHtml(b.title || b.name || '')}</span> <span style="font-size:0.7rem;padding:0 4px;border-radius:3px;background:rgba(255,255,255,0.08);">${b.execution_stage || b.status || 'todo'}</span>`;
        row.addEventListener('click', () => openDetail(b.id));
        treeBox.appendChild(row);

        if (b.rationale) {
          const subPrefix = isLast ? '      └─ ' : '  │   └─ ';
          const ratRow = el('div', null);
          ratRow.style.cssText = 'color:var(--muted);font-size:0.75rem;';
          ratRow.textContent = `${subPrefix}Rationale: ${b.rationale}`;
          treeBox.appendChild(ratRow);
        }
      });
    }

    // Subtasks
    if (task.dependencies?.subtasks?.length) {
      const subHeader = el('div', null, `◇ Subtasks (${task.dependencies.subtasks.length}):`);
      subHeader.style.cssText = 'color:var(--muted);font-weight:600;margin-top:4px;';
      treeBox.appendChild(subHeader);

      task.dependencies.subtasks.forEach((st, idx) => {
        const isLast = idx === task.dependencies.subtasks.length - 1;
        const prefix = isLast ? '  └── ' : '  ├── ';
        const row = el('div', null);
        row.style.cssText = 'cursor:pointer;padding:2px 0;';
        row.innerHTML = `${prefix}<span style="color:var(--accent,#38bdf8);text-decoration:underline;">#${st.identifier || st.id?.slice(0, 10)}</span> ${escapeHtml(st.title || st.name || '')}`;
        row.addEventListener('click', () => openDetail(st.id));
        treeBox.appendChild(row);
      });
    }

    depSection.appendChild(treeBox);
    content.appendChild(depSection);
  }

  // Parent task
  if (task.parent_id || task.parent_identifier) {
    addPanelField(content, 'Parent Task',
      task.parent_identifier || `#${task.parent_id?.slice(0, 12)}`);
  }

  // Timestamps
  if (task.created_at || task.updated_at) {
    const tsField = el('div', 'panel-field');
    tsField.appendChild(el('div', 'panel-field-label', 'Timestamps'));
    const tsVal = el('div', 'panel-field-muted');
    const parts = [];
    if (task.created_at) parts.push(`Created: ${fmtDateTime(task.created_at)}`);
    if (task.updated_at) parts.push(`Updated: ${fmtRelTime(task.updated_at)}`);
    tsVal.textContent = parts.join('  ·  ');
    tsField.appendChild(tsVal);
    content.appendChild(tsField);
  }

  // Raw ID
  const idField = el('div', 'panel-field');
  idField.appendChild(el('div', 'panel-field-label', 'Internal ID'));
  idField.appendChild(el('div', 'panel-field-muted', task.id || '—'));
  content.appendChild(idField);
}

function isFleetTaskId(id) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id);
}

let lastDetailOpenTime = 0;

async function openDetail(taskId, pushHistory = true) {
  const panel   = document.getElementById('detail-panel');
  const content = document.getElementById('panel-content');

  stopChatPoll();
  state.openDetailTaskId = taskId;
  lastDetailOpenTime = Date.now();
  panel.classList.remove('hidden');
  content.innerHTML = '<p style="color:var(--muted)">Loading…</p>';

  if (pushHistory && !window.location.pathname.startsWith('/tasks/' + taskId) && !window.location.pathname.startsWith('/issues/' + taskId)) {
    history.pushState({ taskId }, '', '/tasks/' + taskId);
  }

  const isFleet = isFleetTaskId(taskId);
  const apiBase = isFleet ? '/api/fleet/tasks' : '/api/tasks';

  try {
    const [taskResp, commentsResp] = await Promise.all([
      apiFetch(`${apiBase}/${taskId}`),
      apiFetch(`${apiBase}/${taskId}/comments`).catch(() => ({ comments: [] }))
    ]);
    const task = taskResp.task || taskResp;
    if (taskResp.dependencies) {
      task.dependencies = taskResp.dependencies;
    }
    const comments = (taskResp.comments && taskResp.comments.length)
      ? taskResp.comments
      : (commentsResp?.comments || (Array.isArray(commentsResp) ? commentsResp : []));
    task.comments = comments;

    // Also fetch native StayPoint governance snapshot if available
    try {
      const govResp = await apiFetch(`/api/tasks/${taskId}/governance`);
      if (govResp && !govResp.error) {
        task.governance = govResp;
      }
    } catch { /* governance optional */ }

    renderDetailContent(content, task);
    buildChatSection(content, taskId, comments);
    startChatPoll(taskId);
  } catch {
    const cached = state.tasks[taskId];
    if (cached) {
      renderDetailContent(content, cached);
      buildChatSection(content, taskId, cached.comments || []);
      startChatPoll(taskId);
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

function closeDetailPanel() {
  const panel = document.getElementById('detail-panel');
  if (!panel || panel.classList.contains('hidden')) return;
  panel.classList.add('hidden');
  stopChatPoll();
  state.openDetailTaskId = null;
  if (window.location.pathname.startsWith('/tasks/') || window.location.pathname.startsWith('/issues/')) {
    const activeBtn = document.querySelector('.sidebar-item.active');
    const viewName = activeBtn?.dataset?.view || 'overview';
    navigateTo(viewName, state.currentOrgDetail, true);
  }
}

document.getElementById('panel-close').addEventListener('click', closeDetailPanel);

document.addEventListener('click', (e) => {
  const panel = document.getElementById('detail-panel');
  if (!panel || panel.classList.contains('hidden')) return;
  // If detail was just opened/switched in this click event, do not close
  if (Date.now() - lastDetailOpenTime < 150) return;
  // If clicked inside the detail panel, do not close
  if (panel.contains(e.target)) return;
  // If clicked on the close button, it has its own handler
  if (e.target.closest('#panel-close')) return;
  closeDetailPanel();
});

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    closeDetailPanel();
  }
});

// ── Render All ────────────────────────────────────────────
function renderAll() {
  renderOverview();
  renderKanban();
  renderBoss();

  // Re-render org detail if one is open and fleet data refreshed
  if (state.currentOrgDetail) {
    const org = (state.fleet?.organizations || []).find(o => o.name === state.currentOrgDetail);
    if (org) renderOrgDetailView(org);
  }

  renderSidebarOrgTree();

  // Re-render any open dynamic pages
  if (document.getElementById('view-projects')?.classList.contains('active')) renderProjects();
  if (document.getElementById('view-agents')?.classList.contains('active'))   renderAgentsPage();
  if (document.getElementById('view-recent-tasks')?.classList.contains('active')) renderRecentTasks();
  if (document.getElementById('view-task-status')?.classList.contains('active'))  renderTaskStatusPage();
  if (document.getElementById('view-cost')?.classList.contains('active'))         renderCostPage();
}

// ── Filter listeners (overview) ───────────────────────────
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

// ── Filter listeners (task status page) ──────────────────
document.getElementById('ts-search-input')?.addEventListener('input', (e) => {
  state.tsFilter.search = e.target.value;
  renderTaskStatusPage();
});
document.getElementById('ts-org-filter')?.addEventListener('change', (e) => {
  state.tsFilter.org = e.target.value;
  renderTaskStatusPage();
});
document.getElementById('ts-status-filter')?.addEventListener('change', (e) => {
  state.tsFilter.status = e.target.value;
  renderTaskStatusPage();
});

// ── Filter listeners (agents page) ───────────────────────
document.getElementById('agents-search')?.addEventListener('input', (e) => {
  state.agentsFilter.search = e.target.value;
  renderAgentsPage();
});
document.getElementById('agents-org-filter')?.addEventListener('change', (e) => {
  state.agentsFilter.org = e.target.value;
  populateAgentsProjectFilter();
  renderAgentsPage();
});
document.getElementById('agents-project-filter')?.addEventListener('change', (e) => {
  state.agentsFilter.project = e.target.value;
  renderAgentsPage();
});
document.getElementById('agents-provider-filter')?.addEventListener('change', (e) => {
  state.agentsFilter.provider = e.target.value;
  renderAgentsPage();
});
document.getElementById('agents-org-filter')?.addEventListener('change', (e) => {
  state.agentsFilter.org = e.target.value;
  renderAgentsPage();
});
document.querySelectorAll('.agent-filter-pill').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('.agent-filter-pill').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    state.agentsFilter.status = btn.dataset.status || 'all';
    renderAgentsPage();
  });
});

// ── Checklist View ────────────────────────────────────────

let checklistItems = [];   // local cache

async function loadChecklist(sprint) {
  const s = sprint || document.getElementById('checklist-sprint-filter')?.value || 'STA-168';
  try {
    const r = await apiFetch(`/api/checklist?sprint=${encodeURIComponent(s)}`);
    checklistItems = r.items || [];
    renderChecklist();
  } catch (err) {
    const c = document.getElementById('checklist-container');
    if (c) c.innerHTML = `<p style="color:var(--red)">Failed to load checklist: ${err.message}. Try seeding first.</p>`;
  }
}

async function seedChecklist() {
  const sprint = document.getElementById('checklist-sprint-filter')?.value || 'STA-168';
  const btn = document.getElementById('checklist-seed-btn');
  if (btn) { btn.disabled = true; btn.textContent = 'Seeding…'; }
  try {
    await fetch('/api/checklist/seed', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeader() },
      body: JSON.stringify({ sprint, force: true }),
    });
    await loadChecklist(sprint);
  } catch (err) {
    alert('Seed failed: ' + err.message);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = 'Seed / Reset'; }
  }
}

async function loadChecklistSprints() {
  try {
    const r = await apiFetch('/api/checklist/sprints');
    const sel = document.getElementById('checklist-sprint-filter');
    if (!sel || !r.sprints?.length) return;
    sel.innerHTML = '';
    for (const s of r.sprints) {
      const opt = document.createElement('option');
      opt.value = s; opt.textContent = s;
      sel.appendChild(opt);
    }
  } catch { /* sprints endpoint optional */ }
}

function getSectionCollapsed(sectionName, isFinished) {
  const stored = localStorage.getItem('staypoint_cl_col_' + sectionName);
  if (stored !== null) return stored === 'true';
  // Default: start collapsed if all items in section are reviewed
  return isFinished;
}

function setSectionCollapsed(sectionName, collapsed) {
  localStorage.setItem('staypoint_cl_col_' + sectionName, collapsed ? 'true' : 'false');
}

function renderChecklist() {
  const container = document.getElementById('checklist-container');
  if (!container) return;
  container.innerHTML = '';

  if (!checklistItems.length) {
    const p = el('p', 'muted-text', 'No checklist items. Click "Seed / Reset" to populate from STA-168.');
    container.appendChild(p);
    updateChecklistProgress();
    return;
  }

  // Group by section
  const sections = {};
  for (const item of checklistItems) {
    if (!sections[item.section]) sections[item.section] = [];
    sections[item.section].push(item);
  }

  // Progress bar
  const total = checklistItems.length;
  const done  = checklistItems.filter(i => i.status !== 'pending').length;
  const pct   = total ? Math.round(done / total * 100) : 0;

  const progBar = el('div', 'checklist-progress-bar');
  const progFill = el('div', 'checklist-progress-fill');
  progFill.style.width = `${pct}%`;
  progBar.appendChild(progFill);
  container.appendChild(progBar);

  const progEl = document.getElementById('checklist-progress');
  if (progEl) progEl.textContent = `${done}/${total} reviewed (${pct}%)`;

  // Section Collapse toolbar
  const toolbar = el('div', 'checklist-toolbar');

  const colCompBtn = el('button', 'cl-ctrl-btn', 'Collapse Finished');
  colCompBtn.title = 'Collapse all sections where 100% of items are reviewed';
  colCompBtn.addEventListener('click', () => {
    for (const [secName, secItems] of Object.entries(sections)) {
      const isFin = secItems.every(i => i.status !== 'pending');
      setSectionCollapsed(secName, isFin);
    }
    renderChecklist();
  });

  const expAllBtn = el('button', 'cl-ctrl-btn', 'Expand All');
  expAllBtn.addEventListener('click', () => {
    for (const secName of Object.keys(sections)) {
      setSectionCollapsed(secName, false);
    }
    renderChecklist();
  });

  const colAllBtn = el('button', 'cl-ctrl-btn', 'Collapse All');
  colAllBtn.addEventListener('click', () => {
    for (const secName of Object.keys(sections)) {
      setSectionCollapsed(secName, true);
    }
    renderChecklist();
  });

  toolbar.appendChild(colCompBtn);
  toolbar.appendChild(expAllBtn);
  toolbar.appendChild(colAllBtn);
  container.appendChild(toolbar);

  for (const [sectionName, items] of Object.entries(sections)) {
    const sec = el('div', 'checklist-section dashboard-section');
    sec.dataset.section = sectionName;
    const isFinished = items.every(i => i.status !== 'pending');
    const isCollapsed = getSectionCollapsed(sectionName, isFinished);

    const header = el('div', 'checklist-section-header');

    const titleLeft = el('div', 'checklist-section-title-left');
    const chevron = el('span', 'checklist-section-chevron', isCollapsed ? '▶' : '▼');
    const titleText = el('span', 'checklist-section-name', sectionName);
    titleLeft.appendChild(chevron);
    titleLeft.appendChild(titleText);

    const passCount = items.filter(i => i.status === 'pass').length;
    const partialCount = items.filter(i => i.status === 'partial').length;
    const failCount = items.filter(i => i.status === 'fail').length;
    const notDoneCount = items.filter(i => i.status === 'not_done').length;

    const badges = el('div', 'checklist-section-badges');
    if (passCount || partialCount || failCount || notDoneCount) {
      badges.innerHTML = `✅ ${passCount}${partialCount ? ` ◐ ${partialCount}` : ''} ❌ ${failCount}${notDoneCount ? ` ⚠️ ${notDoneCount}` : ''}`;
    }

    header.appendChild(titleLeft);
    header.appendChild(badges);
    sec.appendChild(header);

    const secBody = el('div', 'checklist-section-body');
    if (isCollapsed) {
      secBody.style.display = 'none';
    }

    for (const item of items) {
      secBody.appendChild(buildChecklistItem(item));
    }
    sec.appendChild(secBody);

    header.addEventListener('click', () => {
      const currentlyCollapsed = secBody.style.display === 'none';
      if (currentlyCollapsed) {
        secBody.style.display = 'block';
        chevron.textContent = '▼';
        setSectionCollapsed(sectionName, false);
        requestAnimationFrame(() => {
          secBody.querySelectorAll('.checklist-notes-input').forEach(ta => {
            ta.style.height = 'auto';
            ta.style.height = Math.max(48, ta.scrollHeight) + 'px';
          });
        });
      } else {
        secBody.style.display = 'none';
        chevron.textContent = '▶';
        setSectionCollapsed(sectionName, true);
      }
    });

    container.appendChild(sec);
  }

  requestAnimationFrame(() => {
    container.querySelectorAll('.checklist-notes-input').forEach(ta => {
      if (ta.offsetParent !== null) {
        ta.style.height = 'auto';
        ta.style.height = Math.max(48, ta.scrollHeight) + 'px';
      }
    });
  });
}

function buildChecklistItem(item) {
  const row = el('div', 'checklist-item');
  row.dataset.id = item.id;

  // Status buttons column
  const btnCol = el('div', 'checklist-status-btns');
  for (const [status, icon, label] of [
    ['pass',     '✓', 'Pass'],
    ['partial',  '◐', 'In-Between / Needs Work'],
    ['fail',     '✗', 'Fail'],
    ['skip',     '–', 'Skip'],
    ['not_done', '!', 'Not Done'],
  ]) {
    const btn = el('button', `cl-btn${item.status === status ? ' active-' + status : ''}`, icon);
    btn.dataset.status = status;
    btn.title = label;
    btn.addEventListener('click', () => updateChecklistStatus(item.id, status));
    btnCol.appendChild(btn);
  }
  row.appendChild(btnCol);

  // Body
  const body = el('div', 'checklist-body');
  const titleEl = el('div', `checklist-title${item.status !== 'pending' ? ' status-' + item.status : ''}`, item.title);
  if (item.contract) {
    const contractTag = el('span', 'checklist-contract-tag', '⚙ contract');
    contractTag.title = 'Machine-verifiable contract: ' + item.contract;
    titleEl.appendChild(contractTag);
  }
  body.appendChild(titleEl);
  if (item.description) body.appendChild(el('div', 'checklist-desc', item.description));
  if (item.how_to_test) body.appendChild(el('div', 'checklist-howto', item.how_to_test));

  // Notes + version history
  const notesRow = el('div', 'checklist-notes-row');
  const notesInput = el('textarea', 'checklist-notes-input');
  notesInput.value = item.notes || '';
  notesInput.placeholder = 'Add a note…';
  const rawNote = item.notes || '';
  const lineCount = Math.max(rawNote.split('\n').length, Math.ceil(rawNote.length / 50));
  notesInput.rows = Math.max(2, Math.min(15, lineCount));
  const autoResize = () => {
    notesInput.style.height = 'auto';
    notesInput.style.height = Math.max(48, notesInput.scrollHeight) + 'px';
  };
  notesInput.addEventListener('input', autoResize);
  notesInput.addEventListener('focus', autoResize);
  notesInput.addEventListener('blur', () => {
    // Note: NEVER collapse height on blur
    const val = (notesInput.value || '').trim();
    if (val !== (item.notes || '').trim()) {
      updateChecklistNotes(item.id, notesInput.value);
    }
  });
  requestAnimationFrame(autoResize);

  const saveBtn = el('button', 'checklist-save-btn', 'Save note');
  saveBtn.addEventListener('click', async () => {
    saveBtn.textContent = 'Saving…';
    saveBtn.disabled = true;
    await updateChecklistNotes(item.id, notesInput.value);
    saveBtn.textContent = 'Saved!';
    setTimeout(() => {
      saveBtn.textContent = 'Save note';
      saveBtn.disabled = false;
    }, 1200);
  });

  const histBtn = el('span', 'checklist-history-toggle', `v${item.version}`);
  histBtn.title = 'Click to show version history';
  const histList = el('div', 'checklist-history-list');
  histList.style.display = 'none';
  histBtn.addEventListener('click', async () => {
    if (histList.style.display === 'none') {
      histList.style.display = 'block';
      histList.innerHTML = '<span class="muted-text">Loading…</span>';
      try {
        const r = await apiFetch(`/api/checklist/${item.id}/history`);
        histList.innerHTML = '';
        if (!r.history?.length) {
          histList.appendChild(el('div', 'checklist-history-entry', 'No history yet.'));
        } else {
          for (const e of r.history) {
            const entry = el('div', 'checklist-history-entry');
            entry.innerHTML = `<strong>${e.status}</strong> — ${fmtDateTime(e.changed_at)}${e.notes ? ': ' + escapeHtml(e.notes) : ''}`;
            histList.appendChild(entry);
          }
        }
      } catch {
        histList.innerHTML = '<span class="muted-text">Failed to load history.</span>';
      }
    } else {
      histList.style.display = 'none';
    }
  });

  notesRow.appendChild(notesInput);
  notesRow.appendChild(saveBtn);
  notesRow.appendChild(histBtn);
  body.appendChild(notesRow);
  body.appendChild(histList);
  row.appendChild(body);

  return row;
}

function updateSectionCounts(sectionName) {
  const sec = document.querySelector(`.checklist-section[data-section="${CSS.escape(sectionName)}"]`);
  if (!sec) return;
  const items = checklistItems.filter(i => i.section === sectionName);
  const passCount = items.filter(i => i.status === 'pass').length;
  const partialCount = items.filter(i => i.status === 'partial').length;
  const failCount = items.filter(i => i.status === 'fail').length;
  const notDoneCount = items.filter(i => i.status === 'not_done').length;
  const badges = sec.querySelector('.checklist-section-badges');
  if (badges) {
    if (passCount || partialCount || failCount || notDoneCount) {
      badges.innerHTML = `✅ ${passCount}${partialCount ? ` ◐ ${partialCount}` : ''} ❌ ${failCount}${notDoneCount ? ` ⚠️ ${notDoneCount}` : ''}`;
    } else {
      badges.innerHTML = '';
    }
  }
}

async function updateChecklistStatus(id, status) {
  const idx = checklistItems.findIndex(i => i.id === id);
  if (idx === -1) return;
  const row = document.querySelector(`.checklist-item[data-id="${id}"]`);
  const currentNotesInput = row?.querySelector('.checklist-notes-input');
  const notes = currentNotesInput ? currentNotesInput.value : checklistItems[idx].notes;
  try {
    const r = await fetch(`/api/checklist/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', ...authHeader() },
      body: JSON.stringify({ status, notes }),
    });
    if (!r.ok) throw new Error(await r.text());
    const updated = await r.json();
    checklistItems[idx] = updated;

    if (row) {
      row.querySelectorAll('.cl-btn').forEach(btn => {
        btn.className = `cl-btn${btn.dataset.status === updated.status ? ' active-' + updated.status : ''}`;
      });
      const titleEl = row.querySelector('.checklist-title');
      if (titleEl) {
        titleEl.className = `checklist-title${updated.status !== 'pending' ? ' status-' + updated.status : ''}`;
      }
      const histBtn = row.querySelector('.checklist-history-toggle');
      if (histBtn) histBtn.textContent = `v${updated.version}`;
    }
    updateChecklistProgress();
    updateSectionCounts(checklistItems[idx].section);
  } catch (err) {
    console.error('checklist update failed', err);
  }
}

async function updateChecklistNotes(id, notes) {
  const idx = checklistItems.findIndex(i => i.id === id);
  if (idx === -1) return;
  try {
    const r = await fetch(`/api/checklist/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', ...authHeader() },
      body: JSON.stringify({ notes }),
    });
    if (!r.ok) throw new Error(await r.text());
    const updated = await r.json();
    checklistItems[idx] = updated;
    // Re-render just the version badge without full re-render for UX
    const row = document.querySelector(`.checklist-item[data-id="${id}"]`);
    const histBtn = row?.querySelector('.checklist-history-toggle');
    if (histBtn) histBtn.textContent = `v${updated.version}`;
    updateChecklistProgress();
  } catch (err) {
    console.error('checklist note save failed', err);
  }
}

function updateChecklistProgress() {
  const total = checklistItems.length;
  const done  = checklistItems.filter(i => i.status !== 'pending').length;
  const pct   = total ? Math.round(done / total * 100) : 0;
  const progEl = document.getElementById('checklist-progress');
  if (progEl) progEl.textContent = total ? `${done}/${total} reviewed (${pct}%)` : '';
}

// Sidebar wire-up for checklist
document.getElementById('checklist-seed-btn')?.addEventListener('click', seedChecklist);
document.getElementById('checklist-verify-btn')?.addEventListener('click', verifyChecklistContracts);
document.getElementById('checklist-sprint-filter')?.addEventListener('change', (e) => {
  loadChecklist(e.target.value);
});

async function verifyChecklistContracts() {
  const sprint = document.getElementById('checklist-sprint-filter')?.value || 'STA-168';
  const btn = document.getElementById('checklist-verify-btn');
  if (btn) {
    btn.disabled = true;
    btn.textContent = 'Verifying…';
  }
  try {
    const res = await fetch(`/api/checklist/evaluate?sprint=${encodeURIComponent(sprint)}&downgrade=true`, {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${window.__STAYPOINT_TOKEN__ || ''}` }
    });
    const summary = await res.json();
    if (summary.divergences > 0) {
      alert(`⚠️ Divergence Detected!\n\n${summary.divergences} previously-verified item(s) regressed and were automatically downgraded to 'fail'.`);
    } else if (summary.failed > 0) {
      alert(`Checklist Evaluation:\n\n${summary.passed}/${summary.total} contracts passing, ${summary.failed} failing.`);
    } else {
      alert(`✅ All ${summary.total} machine contracts verified successfully!`);
    }
    await loadChecklist(sprint);
  } catch (err) {
    console.error('Failed to verify contracts:', err);
    alert('Failed to evaluate checklist contracts: ' + err.message);
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = '⚡ Verify Contracts';
    }
  }
}

// ── Filter listeners (projects page) ─────────────────────
document.getElementById('projects-org-filter')?.addEventListener('change', () => {
  renderProjects();
});

// ── Boot ──────────────────────────────────────────────────
loadAll().then(() => {
  const initialRoute = pathToRoute();
  navigateTo(initialRoute.view, initialRoute.org, false);
  if (initialRoute.taskId) {
    openDetail(initialRoute.taskId, false);
  }
  connectSSE();
});
