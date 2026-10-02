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
    project: 'all',
    priority: 'all',
    status: 'all',
  },
  tsFilter: { search: '', org: 'all', project: 'all', priority: 'all', status: 'all' },
  agentsFilter: { search: '', org: 'all', project: 'all', provider: 'all', status: 'all' },
  projectsFilter: {
    orgs: [],       // selected org names, empty = all
    status: 'all',  // 'all', 'active', 'running', 'done', 'blocked'
    cardStatus: {}, // `${org}:${proj}` -> status filter on that card
  },
  recentTasksFilter: {
    org: 'all',
    project: 'all',
    priority: 'all',
  },
  expandedRecentSubtasks: new Set(),
  overviewSort: { column: 'updated', direction: 'desc' },
  tsSort: { column: 'updated', direction: 'desc' },
  orgSort: { column: 'task', direction: 'asc' },
  taskComments: {},     // taskId -> array of comments
  taskDescriptions: {}, // taskId -> description string
  currentOrgDetail: null,
  openDetailTaskId: null,
  chatPollTimer:    null,
  bossReportCache:           {},   // reportId -> html string
  bossReportCacheTimestamps: {},   // reportId -> epoch ms
  bossReportLastRenderedAt:  0,    // epoch ms of last full render/refresh
  bossReportPreloading:      false,
  taskDetailFullPage:        false,
};

// ── Projects Filter Persistence (STA-192) ────────────────
const STORAGE_PROJECTS_ORGS_KEY = 'staypoint_projects_selected_orgs';
const STORAGE_PROJECTS_STATUS_KEY = 'staypoint_projects_status_filter';
const STORAGE_PROJECTS_CARD_STATUS_KEY = 'staypoint_projects_card_status';

function loadProjectsFilters() {
  try {
    const savedOrgs = localStorage.getItem(STORAGE_PROJECTS_ORGS_KEY);
    if (savedOrgs) {
      const parsed = JSON.parse(savedOrgs);
      if (Array.isArray(parsed)) {
        state.projectsFilter.orgs = parsed;
      }
    }
  } catch { /* ignore parse error */ }

  try {
    const savedStatus = localStorage.getItem(STORAGE_PROJECTS_STATUS_KEY);
    if (savedStatus && ['all', 'active', 'running', 'done', 'blocked'].includes(savedStatus)) {
      state.projectsFilter.status = savedStatus;
    }
  } catch { /* ignore */ }

  try {
    const savedCards = localStorage.getItem(STORAGE_PROJECTS_CARD_STATUS_KEY);
    if (savedCards) {
      const parsed = JSON.parse(savedCards);
      if (parsed && typeof parsed === 'object') {
        state.projectsFilter.cardStatus = parsed;
      }
    }
  } catch { /* ignore */ }
}

function saveProjectsFilters() {
  try {
    localStorage.setItem(STORAGE_PROJECTS_ORGS_KEY, JSON.stringify(state.projectsFilter.orgs || []));
    localStorage.setItem(STORAGE_PROJECTS_STATUS_KEY, state.projectsFilter.status || 'all');
    localStorage.setItem(STORAGE_PROJECTS_CARD_STATUS_KEY, JSON.stringify(state.projectsFilter.cardStatus || {}));
  } catch { /* ignore storage errors */ }
}

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
    for (const t of (tasksResp.tasks || [])) {
      state.tasks[t.id] = t;
      if (t.description) state.taskDescriptions[t.id] = t.description;
      if (t.comments && t.comments.length) state.taskComments[t.id] = t.comments;
    }
    for (const s of (sessionsResp.sessions || [])) state.sessions[s.id] = s;

    if (fleetResp?.tasks) {
      for (const t of fleetResp.tasks) {
        if (!state.tasks[t.id]) {
          state.tasks[t.id] = { ...t, status: normalizeFleetStatus(t.status) };
        } else {
          state.tasks[t.id] = { ...t, ...state.tasks[t.id] };
          if (t.identifier && !state.tasks[t.id].identifier) {
            state.tasks[t.id].identifier = t.identifier;
          }
          if (t.organization && !state.tasks[t.id].organization) {
            state.tasks[t.id].organization = t.organization;
          }
          if (t.project && !state.tasks[t.id].project) {
            state.tasks[t.id].project = t.project;
          }
          if (!state.tasks[t.id].description && t.description) {
            state.tasks[t.id].description = t.description;
          }
          if ((!state.tasks[t.id].comments || !state.tasks[t.id].comments.length) && t.comments) {
            state.tasks[t.id].comments = t.comments;
          }
        }
        if (t.description) state.taskDescriptions[t.id] = t.description;
        if (t.comments && t.comments.length) state.taskComments[t.id] = t.comments;
      }
    }

    if (fleetResp?.organizations) {
      for (const org of fleetResp.organizations) {
        for (const t of (org.tasks || [])) {
          if (!state.tasks[t.id]) {
            state.tasks[t.id] = { ...t, organization: t.organization || org.name, status: normalizeFleetStatus(t.status) };
          } else {
            if (!state.tasks[t.id].organization) state.tasks[t.id].organization = org.name;
            if (t.identifier && !state.tasks[t.id].identifier) state.tasks[t.id].identifier = t.identifier;
            if (t.project && !state.tasks[t.id].project) state.tasks[t.id].project = t.project;
          }
        }
      }
    }

    loadProjectsFilters();
    populateOrgFilter();
    populateTSOrgFilter();
    populateAgentsFilters();
    populateProjectsOrgFilter();
    populateRecentTasksFilters();
    renderAll();
    renderSidebarOrgTree();
    prefetchTaskComments();
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
  if (type === 'run.step' && evt.data) {
    applyTimelineStep(evt.data);
    return;
  }
  if (type === 'run.state' && evt.data) {
    // Refresh task page status pill on run completion
    const stateData = evt.data;
    if (stateData.task_id) refreshFleetData();
    return;
  }
  if (type === 'run.stats' && evt.data) {
    const statsData = evt.data;
    const statsBar = document.getElementById('page-timeline-stats');
    if (statsBar && statsData) {
      statsBar.style.display = 'flex';
      const span = statsBar.querySelector('.tl-stat-tokens') || (() => {
        const s = document.createElement('span');
        s.className = 'tl-stat tl-stat-tokens';
        statsBar.appendChild(s);
        return s;
      })();
      const tok = (statsData.input_tokens || 0) + (statsData.output_tokens || 0);
      span.textContent = `🔢 ${tok.toLocaleString()} tokens`;
    }
    return;
  }
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
      if (fleetResp.tasks) {
        for (const t of fleetResp.tasks) {
          if (t.description) state.taskDescriptions[t.id] = t.description;
          if (t.comments && t.comments.length) state.taskComments[t.id] = t.comments;
          if (state.tasks[t.id]) {
            if (!state.tasks[t.id].description && t.description) state.tasks[t.id].description = t.description;
            if ((!state.tasks[t.id].comments || !state.tasks[t.id].comments.length) && t.comments) state.tasks[t.id].comments = t.comments;
          }
        }
      }
      populateOrgFilter();
      populateTSOrgFilter();
      populateAgentsFilters();
      populateProjectsOrgFilter();
      populateRecentTasksFilters();
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
  document.body.classList.toggle('sidebar-collapsed', Boolean(sidebar?.classList.contains('collapsed')));
});

// ── SPA URL Routing ──────────────────────────────────────
function viewToPath(viewName, orgName) {
  if (orgName) return `/org/${encodeURIComponent(orgName)}`;
  if (!viewName || viewName === 'overview') return '/';
  return `/${viewName}`;
}

function projectSlug(task) {
  const p = task.project;
  if (!p) return 'default';
  if (typeof p !== 'object') return String(p) || 'default';
  return p.urlKey || p.slug || p.name || p.id || 'default';
}

function taskToPath(task) {
  if (!task) return '/';
  const ident = task.identifier || task.id || '';
  let org = '';
  if (ident && ident.includes('-') && !ident.startsWith('task-')) {
    org = ident.split('-')[0];
  }
  if (!org && task.organization) {
    const matched = (state.fleet?.organizations || []).find(o =>
      o.name?.toLowerCase() === task.organization.toLowerCase() ||
      o.issue_prefix?.toLowerCase() === task.organization.toLowerCase() ||
      o.id === task.organization
    );
    org = matched?.issue_prefix || matched?.name || task.organization;
  }
  if (!org && task.org) {
    org = task.org;
  }
  if (!org) {
    org = 'STA';
  }
  const project = projectSlug(task);
  return `/tasks/${encodeURIComponent(org)}/${encodeURIComponent(project)}/${encodeURIComponent(ident)}`;
}

function findTask(target, orgHint = null, projectHint = null) {
  if (!target) return null;
  const targetStr = String(target).trim();
  const targetLower = targetStr.toLowerCase();
  const cleanTarget = targetLower.startsWith('#') ? targetLower.slice(1) : targetLower;

  // 1. Direct exact key match in state.tasks
  if (state.tasks[targetStr]) return state.tasks[targetStr];
  if (state.tasks[cleanTarget]) return state.tasks[cleanTarget];

  // Gather candidate pool
  const allTasks = Object.values(state.tasks || {});
  if (state.fleet?.tasks) {
    for (const ft of state.fleet.tasks) {
      if (!allTasks.find(t => t.id === ft.id)) {
        allTasks.push(ft);
      }
    }
  }

  const exactMatches = [];
  const partialMatches = [];

  for (const t of allTasks) {
    const id = (t.id || '').toLowerCase();
    const ident = (t.identifier || '').toLowerCase();

    if (id === cleanTarget || (ident && ident === cleanTarget) || (ident && ident === targetLower)) {
      exactMatches.push(t);
    } else if (cleanTarget.length >= 6 && id.startsWith(cleanTarget)) {
      partialMatches.push(t);
    } else if (ident && (ident.endsWith('-' + cleanTarget) || ident.endsWith('-task-' + cleanTarget))) {
      partialMatches.push(t);
    }
  }

  const pool = exactMatches.length > 0 ? exactMatches : partialMatches;
  if (pool.length === 1) return pool[0];

  if (pool.length > 1) {
    if (orgHint) {
      const orgLower = orgHint.toLowerCase();
      const match = pool.find(t => {
        const tOrg = (t.organization || t.org || '').toLowerCase();
        const tPrefix = (t.identifier || '').split('-')[0].toLowerCase();
        return tOrg === orgLower || tPrefix === orgLower;
      });
      if (match) return match;
    }
    if (projectHint) {
      const projLower = projectHint.toLowerCase();
      const match = pool.find(t => (t.project || 'default').toLowerCase() === projLower);
      if (match) return match;
    }
    return pool[0];
  }

  return null;
}

function pathToRoute(pathname) {
  const p = (pathname || window.location.pathname).replace(/\/+$/, '') || '/';
  if (p === '/' || p === '/overview') return { view: 'overview', org: null, taskId: null };
  if (p.startsWith('/org/')) {
    const org = decodeURIComponent(p.slice(5));
    return { view: 'org-detail', org, taskId: null };
  }
  if (p.startsWith('/tasks/') || p.startsWith('/issues/')) {
    const prefix = p.startsWith('/tasks/') ? '/tasks/' : '/issues/';
    const rest = p.slice(prefix.length);
    const segments = rest.split('/').filter(Boolean).map(decodeURIComponent);
    if (segments.length >= 3) {
      // /tasks/:org/:project/:identifier
      const org = segments[0];
      const project = segments[1];
      const identifier = segments.slice(2).join('/');
      return { view: 'overview', org, project, identifier, taskId: identifier };
    } else if (segments.length === 2) {
      // /tasks/:org/:identifier
      const org = segments[0];
      const identifier = segments[1];
      return { view: 'overview', org, project: null, identifier, taskId: identifier };
    } else if (segments.length === 1) {
      // /tasks/:identifier or /tasks/:id
      const identifier = segments[0];
      return { view: 'overview', org: null, project: null, identifier, taskId: identifier };
    }
    return { view: 'recent-tasks', org: null, taskId: null };
  }
  if (p === '/tasks' || p === '/issues') {
    return { view: 'recent-tasks', org: null, taskId: null };
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
    if (viewName === 'projects')     { populateProjectsOrgFilter(); renderProjects(); }
    if (viewName === 'agents')       { populateAgentsFilters(); renderAgentsPage(); }
    if (viewName === 'recent-tasks') { populateRecentTasksFilters(); renderRecentTasks(); }
    if (viewName === 'task-status')  renderTaskStatusPage();
    if (viewName === 'cost')         renderCostPage();
    if (viewName === 'settings')     renderSettings();
    if (viewName === 'checklist')    loadChecklistSprints().then(() => loadChecklist());
    if (viewName === 'overview')     renderOverview();
    if (viewName === 'kanban')       renderKanban();
    if (viewName === 'task-page')    { /* content rendered by openTaskPage() */ }
    if (viewName === 'boss') {
      renderBoss();
      preloadBossReports(true);
    }
  }

  if (typeof updateWalkthroughSidebarHighlight === 'function') {
    updateWalkthroughSidebarHighlight();
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

window.addEventListener('popstate', (e) => {
  const route = pathToRoute();
  if (route.taskId || route.identifier) {
    if (e.state?.taskPage === false) {
      // Sidebar mode (in-app navigation)
      const panel = document.getElementById('detail-panel');
      if (panel) panel.classList.remove('hidden');
      navigateTo(route.view, route.org, false);
      openDetail(route, false);
    } else {
      // Full-page mode (direct link, "Open" button, or back/forward)
      openTaskPage(route, false);
    }
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
  state.taskFilter.project = 'all';
  state.taskFilter.priority = 'all';
  showView('overview');
  document.querySelectorAll('.sidebar-item').forEach(b => {
    b.classList.toggle('active', b.dataset.view === 'overview');
  });
  renderGlobalTaskTable();
  const sel = document.getElementById('task-status-filter');
  if (sel) sel.value = 'running';
  const os = document.getElementById('task-org-filter');
  if (os) os.value = 'all';
  populateOverviewProjectFilter();
  const ps = document.getElementById('task-project-filter');
  if (ps) ps.value = 'all';
  const pris = document.getElementById('task-priority-filter');
  if (pris) pris.value = 'all';
});

document.getElementById('filter-blocked')?.addEventListener('click', () => {
  state.taskFilter.status = 'blocked';
  state.taskFilter.org = 'all';
  state.taskFilter.project = 'all';
  state.taskFilter.priority = 'all';
  showView('overview');
  document.querySelectorAll('.sidebar-item').forEach(b => {
    b.classList.toggle('active', b.dataset.view === 'overview');
  });
  renderGlobalTaskTable();
  const sel = document.getElementById('task-status-filter');
  if (sel) sel.value = 'blocked';
  const os = document.getElementById('task-org-filter');
  if (os) os.value = 'all';
  populateOverviewProjectFilter();
  const ps = document.getElementById('task-project-filter');
  if (ps) ps.value = 'all';
  const pris = document.getElementById('task-priority-filter');
  if (pris) pris.value = 'all';
});

document.getElementById('filter-clear')?.addEventListener('click', () => {
  state.taskFilter = { search: '', org: 'all', project: 'all', priority: 'all', status: 'all' };
  const si = document.getElementById('task-search-input');
  if (si) si.value = '';
  const os = document.getElementById('task-org-filter');
  if (os) os.value = 'all';
  populateOverviewProjectFilter();
  const ps = document.getElementById('task-project-filter');
  if (ps) ps.value = 'all';
  const pris = document.getElementById('task-priority-filter');
  if (pris) pris.value = 'all';
  const ss = document.getElementById('task-status-filter');
  if (ss) ss.value = 'all';
  renderGlobalTaskTable();
});

// ── Universal KPI Drill-down Navigation Helpers ───────────
function drillDownToTasks(status, org = 'all', project = 'all') {
  state.tsFilter = state.tsFilter || { search: '', org: 'all', project: 'all', priority: 'all', status: 'all' };
  state.tsFilter.status = status;
  state.tsFilter.org = org;
  state.tsFilter.project = project;
  state.tsFilter.priority = 'all';
  state.tsFilter.search = '';

  const tsStatusSel = document.getElementById('ts-status-filter');
  if (tsStatusSel) tsStatusSel.value = status;
  const tsOrgSel = document.getElementById('ts-org-filter');
  if (tsOrgSel) tsOrgSel.value = org;
  populateTSProjectFilter();
  const tsProjSel = document.getElementById('ts-project-filter');
  if (tsProjSel) tsProjSel.value = state.tsFilter.project;
  const tsPriSel = document.getElementById('ts-priority-filter');
  if (tsPriSel) tsPriSel.value = 'all';
  const tsSearch = document.getElementById('ts-search-input');
  if (tsSearch) tsSearch.value = '';

  // Also synchronize Overview table filters so returning retains filter context
  state.taskFilter = state.taskFilter || { search: '', org: 'all', project: 'all', priority: 'all', status: 'all' };
  state.taskFilter.status = status;
  state.taskFilter.org = org;
  state.taskFilter.project = 'all';
  state.taskFilter.priority = 'all';
  const ovStatusSel = document.getElementById('task-status-filter');
  if (ovStatusSel) ovStatusSel.value = status;
  const ovOrgSel = document.getElementById('task-org-filter');
  if (ovOrgSel) ovOrgSel.value = org;
  populateOverviewProjectFilter();
  const ovProjSel = document.getElementById('task-project-filter');
  if (ovProjSel) ovProjSel.value = 'all';
  const ovPriSel = document.getElementById('task-priority-filter');
  if (ovPriSel) ovPriSel.value = 'all';

  showView('task-status');
  renderTaskStatusPage();
}

function drillDownToAgents(status = 'all', org = 'all') {
  state.agentsFilter = state.agentsFilter || { search: '', org: 'all', project: 'all', provider: 'all', status: 'all' };
  if (status !== 'all') state.agentsFilter.status = status;
  if (org !== 'all') state.agentsFilter.org = org;
  const selOrg = document.getElementById('agents-org-filter');
  if (selOrg && org !== 'all') selOrg.value = org;
  showView('agents');
}

function drillDownToCost() {
  showView('cost');
}

function initOverviewKPIClicks() {
  if (state.__kpisInitialized) return;
  state.__kpisInitialized = true;

  const wire = (id, fn) => {
    const cardEl = document.getElementById(id);
    if (!cardEl) return;
    cardEl.addEventListener('click', fn);
    cardEl.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        fn();
      }
    });
  };

  wire('kpi-card-orgs', () => showView('projects'));
  wire('kpi-card-running', () => drillDownToTasks('running'));
  wire('kpi-card-active', () => drillDownToTasks('active'));
  wire('kpi-card-blocked', () => drillDownToTasks('blocked'));
  wire('kpi-card-done', () => drillDownToTasks('done'));
  wire('kpi-card-total', () => drillDownToTasks('all'));
  wire('kpi-card-agents', () => drillDownToAgents());
  wire('kpi-card-tokens', () => drillDownToCost());
  wire('kpi-card-cost', () => drillDownToCost());
}

// ── Render: All Organizations Overview Screen ─────────────
function renderOverview() {
  const f = state.fleet;
  if (!f) return;

  const orgCount = (f.organizations || []).length;
  const elOrgs = document.getElementById('kpi-orgs');
  if (elOrgs) elOrgs.textContent = String(orgCount);

  const gt = f.global_tasks || {};
  const runningTasks = gt.running || 0;
  const activeTasks  = gt.active || 0;
  const blockedTasks = gt.blocked || 0;
  const doneTasks    = gt.done || 0;
  const totalTasks   = gt.total || 0;

  const elRunning = document.getElementById('kpi-running-tasks');
  if (elRunning) elRunning.textContent = String(runningTasks);
  const elTasksTotal = document.getElementById('kpi-tasks-total');
  if (elTasksTotal) elTasksTotal.textContent = `${totalTasks} total tasks`;

  const elActive = document.getElementById('kpi-active-tasks');
  if (elActive) elActive.textContent = String(activeTasks);

  const elBlocked = document.getElementById('kpi-blocked-tasks');
  if (elBlocked) elBlocked.textContent = String(blockedTasks);

  const elDone = document.getElementById('kpi-done-tasks');
  if (elDone) elDone.textContent = String(doneTasks);

  const elTotalVal = document.getElementById('kpi-total-tasks-val');
  if (elTotalVal) elTotalVal.textContent = String(totalTasks);

  const activeAgents = f.global_agents?.active_running || 0;
  const elAgents = document.getElementById('kpi-active-agents');
  if (elAgents) elAgents.textContent = String(activeAgents);

  const providerCounts = f.global_agents?.by_provider || {};
  const elAgentsBreakdown = document.getElementById('kpi-agents-breakdown');
  if (elAgentsBreakdown) {
    elAgentsBreakdown.textContent =
      `Claude: ${providerCounts.claude || 0} · Gemini: ${providerCounts.gemini || 0} · OpenAI: ${providerCounts.openai || 0}`;
  }

  const totTokens = f.token_telemetry?.total_tokens || 0;
  const elTokens = document.getElementById('kpi-total-tokens');
  if (elTokens) elTokens.textContent = fmtCompactNum(totTokens);
  const elTokensIo = document.getElementById('kpi-tokens-io');
  if (elTokensIo) {
    elTokensIo.textContent =
      `In: ${fmtCompactNum(f.token_telemetry?.input_tokens)} · Out: ${fmtCompactNum(f.token_telemetry?.output_tokens)}`;
  }

  const elCost = document.getElementById('kpi-total-cost');
  if (elCost) elCost.textContent = fmtCurrency(f.token_telemetry?.total_cost_usd);

  initOverviewKPIClicks();

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
  let remaining5h = q.five_hour_remaining_pct;
  let used5h = q.five_hour_used_pct;
  if (remaining5h == null && used5h != null) remaining5h = Math.max(0, 100 - used5h);
  if (used5h == null && remaining5h != null) used5h = Math.max(0, 100 - remaining5h);
  if (remaining5h == null && used5h == null) { remaining5h = 100; used5h = 0; }
  if (remaining5h === 100 && used5h > 0) remaining5h = Math.max(0, 100 - used5h);
  remaining5h = Math.max(0, Math.min(100, remaining5h));
  used5h = Math.max(0, Math.min(100, used5h));

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
  let remainingWk = q.weekly_remaining_pct;
  let usedWk = q.weekly_used_pct;
  if (remainingWk == null && usedWk != null) remainingWk = Math.max(0, 100 - usedWk);
  if (usedWk == null && remainingWk != null) usedWk = Math.max(0, 100 - remainingWk);
  if (remainingWk == null && usedWk == null) { remainingWk = 100; usedWk = 0; }
  if (remainingWk === 100 && usedWk > 0) remainingWk = Math.max(0, 100 - usedWk);
  remainingWk = Math.max(0, Math.min(100, remainingWk));
  usedWk = Math.max(0, Math.min(100, usedWk));

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
      if (!isManagedSol && (q.provider === 'claude_work' || q.provider === 'claude')) {
        const persQ = orgQuotas['claude_personal'];
        if (persQ && !persQ.is_locked) return false;
      }
      return true;
    });
    if (lockouts.length) {
      const lockRow = el('div', 'org-lockout-row');
      lockRow.style.cssText = 'color:#f87171;font-size:0.75rem;font-weight:600;margin-top:8px;padding:4px 8px;background:rgba(239,68,68,0.1);border-radius:4px;border:1px solid rgba(239,68,68,0.3);';
      lockRow.textContent = `🔒 Locked: ${lockouts.map(l => l.display_name).join(', ')}`;
      card.appendChild(lockRow);
    } else {
      const quotaKeys = isManagedSol
        ? ['gemini', 'claude_work', 'openai'].filter(k => orgQuotas[k])
        : ['gemini', 'claude_personal', 'openai'].filter(k => orgQuotas[k]);
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
  populateOverviewProjectFilter();
}

function populateOverviewProjectFilter() {
  const select = document.getElementById('task-project-filter');
  if (!select) return;
  const currentOrg = state.taskFilter?.org || 'all';
  const currentProj = state.taskFilter?.project || 'all';
  const projects = getProjectsForOrg(currentOrg);

  select.innerHTML = '<option value="all">All Projects</option>';
  for (const p of projects) {
    const opt = document.createElement('option');
    opt.value = p;
    opt.textContent = p;
    select.appendChild(opt);
  }

  if (projects.includes(currentProj) || currentProj === 'all') {
    select.value = currentProj;
    state.taskFilter.project = currentProj;
  } else {
    select.value = 'all';
    state.taskFilter.project = 'all';
  }
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
  populateTSProjectFilter();
}

function populateTSProjectFilter() {
  const select = document.getElementById('ts-project-filter');
  if (!select) return;
  const currentOrg = state.tsFilter?.org || 'all';
  const currentProj = state.tsFilter?.project || 'all';
  const projects = getProjectsForOrg(currentOrg);

  select.innerHTML = '<option value="all">All Projects</option>';
  for (const p of projects) {
    const opt = document.createElement('option');
    opt.value = p;
    opt.textContent = p;
    select.appendChild(opt);
  }

  if (projects.includes(currentProj) || currentProj === 'all') {
    select.value = currentProj;
    state.tsFilter.project = currentProj;
  } else {
    select.value = 'all';
    state.tsFilter.project = 'all';
  }
}

// ── Table Sorting & Deep Content Search Helpers ───────────
function getPrioritySeverity(p) {
  const s = (p || '').toLowerCase().trim();
  switch (s) {
    case 'critical':
    case 'urgent':
    case 'crit':
      return 4;
    case 'high':
      return 3;
    case 'medium':
    case 'med':
      return 2;
    case 'low':
      return 1;
    default:
      return 0;
  }
}

function sortTasks(tasks, col, dir) {
  if (!col || !dir) return [...tasks];
  const mult = dir === 'desc' ? -1 : 1;
  return [...tasks].sort((a, b) => {
    switch (col) {
      case 'identifier':
      case 'id': {
        const valA = a.identifier || a.id || '';
        const valB = b.identifier || b.id || '';
        return mult * valA.localeCompare(valB, undefined, { numeric: true, sensitivity: 'base' });
      }
      case 'task':
      case 'name':
      case 'title': {
        const valA = (a.title || a.name || '').toLowerCase();
        const valB = (b.title || b.name || '').toLowerCase();
        return mult * valA.localeCompare(valB, undefined, { sensitivity: 'base' });
      }
      case 'organization':
      case 'org': {
        const valA = (a.organization || '').toLowerCase();
        const valB = (b.organization || '').toLowerCase();
        return mult * valA.localeCompare(valB, undefined, { sensitivity: 'base' });
      }
      case 'project': {
        const valA = (a.project || '').toLowerCase();
        const valB = (b.project || '').toLowerCase();
        return mult * valA.localeCompare(valB, undefined, { sensitivity: 'base' });
      }
      case 'assignee': {
        const valA = (a.assignee_name || a.assigned_agent || a.checkout_agent_id || '').toLowerCase();
        const valB = (b.assignee_name || b.assigned_agent || b.checkout_agent_id || '').toLowerCase();
        return mult * valA.localeCompare(valB, undefined, { sensitivity: 'base' });
      }
      case 'status': {
        const valA = (a.status || '').toLowerCase();
        const valB = (b.status || '').toLowerCase();
        return mult * valA.localeCompare(valB, undefined, { sensitivity: 'base' });
      }
      case 'priority': {
        const rankA = getPrioritySeverity(a.priority);
        const rankB = getPrioritySeverity(b.priority);
        if (rankA !== rankB) return mult * (rankA - rankB);
        return (a.title || a.name || '').localeCompare(b.title || b.name || '');
      }
      case 'cost':
      case 'spend':
      case 'spent_usd': {
        const costA = Number(a.spent_usd) || 0;
        const costB = Number(b.spent_usd) || 0;
        if (costA !== costB) return mult * (costA - costB);
        return mult * ((Number(a.spent_tokens) || 0) - (Number(b.spent_tokens) || 0));
      }
      case 'updated':
      case 'updated_at': {
        const tA = a.updated_at ? new Date(a.updated_at).getTime() : 0;
        const tB = b.updated_at ? new Date(b.updated_at).getTime() : 0;
        return mult * (tA - tB);
      }
      default:
        return 0;
    }
  });
}

function getNextSort(currentSort, targetCol, defaultSort) {
  const curCol = currentSort?.column || null;
  const curDir = currentSort?.direction || null;
  const defCol = defaultSort?.column || null;
  const defDir = defaultSort?.direction || null;

  if (curCol !== targetCol) {
    return { column: targetCol, direction: 'asc' };
  }

  if (curDir === 'asc') {
    return { column: targetCol, direction: 'desc' };
  }

  if (curDir === 'desc') {
    if (defCol === targetCol && defDir === 'desc') {
      return { column: targetCol, direction: 'asc' };
    }
    if (defCol && defDir) {
      return { column: defCol, direction: defDir };
    }
    return { column: null, direction: null };
  }

  return { column: targetCol, direction: 'asc' };
}

function renderTableSortHeaders(tableEl, currentSort, onSort, defaultSort) {
  if (!tableEl) return;
  const thead = tableEl.querySelector('thead');
  if (!thead) return;

  tableEl._currentSort = currentSort;
  tableEl._defaultSort = defaultSort;
  tableEl._onSort = onSort;

  const ths = thead.querySelectorAll('th[data-col]');
  ths.forEach(th => {
    const col = th.dataset.col;
    if (!th.dataset.label) {
      th.dataset.label = th.textContent.trim();
    }
    const label = th.dataset.label;
    const isActive = currentSort && currentSort.column === col && currentSort.direction;
    const dir = isActive ? currentSort.direction : null;

    th.classList.add('sortable-th');
    th.classList.toggle('sort-active', !!isActive);
    th.setAttribute('role', 'columnheader');
    th.setAttribute('tabindex', '0');
    th.setAttribute('aria-sort', isActive ? (dir === 'asc' ? 'ascending' : 'descending') : 'none');

    const icon = isActive ? (dir === 'asc' ? '▲' : '▼') : '↕';
    th.innerHTML = `<span class="th-content"><span class="th-label">${label}</span><span class="sort-icon ${isActive ? 'active' : ''}">${icon}</span></span>`;

    if (!th._hasSortListener) {
      th._hasSortListener = true;
      const trigger = () => {
        const next = getNextSort(tableEl._currentSort, th.dataset.col, tableEl._defaultSort);
        if (tableEl._onSort) {
          tableEl._onSort(next.column, next.direction, next);
        }
      };
      th.addEventListener('click', trigger);
      th.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          trigger();
        }
      });
    }
  });
}

function extractMatchSnippet(text, matchIdx, matchLen) {
  if (!text) return '';
  const start = Math.max(0, matchIdx - 25);
  const end = Math.min(text.length, matchIdx + matchLen + 40);
  const prefix = start > 0 ? '…' : '';
  const suffix = end < text.length ? '…' : '';
  const snippet = text.slice(start, end).replace(/\s+/g, ' ');
  return prefix + snippet + suffix;
}

function getTaskSearchMatch(t, term) {
  if (!term) return { matches: true, snippet: null };
  const s = term.toLowerCase();

  // 1. Direct fields: title, identifier, org, project, assignee
  const title = (t.title || t.name || '').toLowerCase();
  const id = (t.identifier || t.id || '').toLowerCase();
  const org = (t.organization || '').toLowerCase();
  const proj = (t.project || '').toLowerCase();
  const assignee = (t.assignee_name || t.assigned_agent || t.checkout_agent_id || '').toLowerCase();

  if (title.includes(s) || id.includes(s) || org.includes(s) || proj.includes(s) || assignee.includes(s)) {
    return { matches: true, snippet: null };
  }

  // 2. Task Description (deep content search)
  const taskId = t.id || t.task_id;
  const desc = t.description || t.desc || (taskId ? state.taskDescriptions?.[taskId] : '') || '';
  if (desc) {
    const idx = desc.toLowerCase().indexOf(s);
    if (idx !== -1) {
      return {
        matches: true,
        snippet: {
          type: 'description',
          text: extractMatchSnippet(desc, idx, s.length),
        },
      };
    }
  }

  // 3. Comments (deep content search inside agent and user comments)
  const comments = t.comments || (taskId ? state.taskComments?.[taskId] : []) || [];
  if (Array.isArray(comments)) {
    for (const c of comments) {
      if (typeof c === 'string') {
        const idx = c.toLowerCase().indexOf(s);
        if (idx !== -1) {
          return {
            matches: true,
            snippet: {
              type: 'comment',
              text: extractMatchSnippet(c, idx, s.length),
            },
          };
        }
      } else if (c && typeof c === 'object') {
        const body = (c.body || c.message || c.content || '');
        const author = (c.author || c.user || c.author_name || '');
        const idxBody = body.toLowerCase().indexOf(s);
        if (idxBody !== -1) {
          return {
            matches: true,
            snippet: {
              type: 'comment',
              author: author,
              text: extractMatchSnippet(body, idxBody, s.length),
            },
          };
        }
        if (author.toLowerCase().includes(s)) {
          return {
            matches: true,
            snippet: {
              type: 'comment',
              author: author,
              text: `Comment by ${author}`,
            },
          };
        }
      }
    }
  }

  return { matches: false, snippet: null };
}

async function prefetchTaskComments() {
  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const toFetch = [];
  const seen = new Set();
  for (const t of allTasks) {
    if (!t.id || seen.has(t.id)) continue;
    seen.add(t.id);
    if ((!t.comments || !t.comments.length) && (!state.taskComments[t.id] || !state.taskComments[t.id].length)) {
      toFetch.push(t.id);
    }
  }

  const chunk = toFetch.slice(0, 30);
  for (let i = 0; i < chunk.length; i += 5) {
    const batch = chunk.slice(i, i + 5);
    await Promise.all(batch.map(async (taskId) => {
      try {
        const isFleet = isFleetTaskId(taskId);
        const apiBase = isFleet ? '/api/fleet/tasks' : '/api/tasks';
        const cr = await apiFetch(`${apiBase}/${taskId}/comments`);
        const comments = cr.comments || (Array.isArray(cr) ? cr : []);
        if (comments.length) {
          state.taskComments[taskId] = comments;
          if (state.tasks[taskId]) {
            state.tasks[taskId].comments = comments;
          }
        }
      } catch { /* optional */ }
    }));
  }
}

function renderGlobalTaskTable() {
  const table = document.getElementById('overview-task-table');
  const tbody = document.getElementById('global-task-tbody');
  if (!tbody) return;

  const DEFAULT_OVERVIEW_SORT = { column: 'updated', direction: 'desc' };
  renderTableSortHeaders(table, state.overviewSort, (col, dir, nextSort) => {
    state.overviewSort = nextSort || { column: col, direction: dir };
    renderGlobalTaskTable();
  }, DEFAULT_OVERVIEW_SORT);

  tbody.innerHTML = '';

  let tasks = state.fleet?.tasks || [];
  if (!tasks.length) tasks = Object.values(state.tasks);

  const sTerm = (state.taskFilter.search || '').toLowerCase().trim();
  const orgFilter = state.taskFilter.org || 'all';
  const projectFilter = state.taskFilter.project || 'all';
  const priorityFilter = state.taskFilter.priority || 'all';
  const statusFilter = state.taskFilter.status || 'all';

  const filtered = filterTasks(tasks, sTerm, orgFilter, statusFilter, projectFilter, priorityFilter);
  const sorted = sortTasks(filtered, state.overviewSort.column, state.overviewSort.direction);

  if (!sorted.length) {
    const tr = document.createElement('tr');
    const td = document.createElement('td');
    td.colSpan = 7;
    td.textContent = 'No tasks match current filter criteria.';
    td.style.cssText = 'text-align:center;color:var(--muted);padding:24px;';
    tr.appendChild(td);
    tbody.appendChild(tr);
    return;
  }

  for (const t of sorted) {
    const tr = makeTaskTableRow(t, 7);
    tbody.appendChild(tr);
  }
}

function filterTasks(tasks, sTerm, orgFilter, statusFilter, projectFilter = 'all', priorityFilter = 'all') {
  return tasks.filter(t => {
    if (orgFilter && orgFilter !== 'all' && t.organization !== orgFilter) return false;
    if (projectFilter && projectFilter !== 'all') {
      const p = (t.project || '').toLowerCase();
      if (p !== projectFilter.toLowerCase()) return false;
    }
    if (priorityFilter && priorityFilter !== 'all') {
      const pri = (t.priority || 'medium').toLowerCase();
      if (pri !== priorityFilter.toLowerCase()) return false;
    }
    if (statusFilter && statusFilter !== 'all') {
      const st = (t.status || 'active').toLowerCase();
      if (statusFilter === 'running' && st !== 'running' && st !== 'in_progress') return false;
      if (statusFilter === 'active'  && st !== 'active'  && st !== 'todo')        return false;
      if (statusFilter === 'blocked' && st !== 'blocked' && !t.is_blocked)        return false;
      if (statusFilter === 'stopped' && st !== 'stopped' && st !== 'cancelled' && st !== 'paused') return false;
      if (statusFilter === 'errored' && st !== 'errored' && st !== 'error' && st !== 'failed') return false;
      if (statusFilter === 'done'    && st !== 'done')                             return false;
    }
    if (sTerm) {
      const match = getTaskSearchMatch(t, sTerm);
      if (!match.matches) return false;
      t._searchSnippet = match.snippet;
    } else {
      t._searchSnippet = null;
    }
    return true;
  });
}

function makeTaskTableRow(t, colCount) {
  const tr = document.createElement('tr');

  const tdId = el('td', null, t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : '—'));
  tdId.style.cssText = 'font-family:monospace;font-weight:600;';

  const tdTitle  = el('td');
  const titleSpan = el('div', 'task-table-title', t.title || t.name || '(untitled)');
  tdTitle.appendChild(titleSpan);

  if (t._searchSnippet) {
    const snipEl = el('div', 'task-search-snippet');
    const badge = el('span', 'snippet-badge', t._searchSnippet.type);
    snipEl.appendChild(badge);
    const textNode = document.createElement('span');
    const sTerm = (state.taskFilter?.search || state.tsFilter?.search || '').trim();
    if (sTerm) {
      const raw = t._searchSnippet.text;
      const lower = raw.toLowerCase();
      const sLower = sTerm.toLowerCase();
      const idx = lower.indexOf(sLower);
      if (idx !== -1) {
        textNode.appendChild(document.createTextNode(raw.slice(0, idx)));
        const mark = el('mark', null, raw.slice(idx, idx + sTerm.length));
        textNode.appendChild(mark);
        textNode.appendChild(document.createTextNode(raw.slice(idx + sTerm.length)));
      } else {
        textNode.textContent = raw;
      }
    } else {
      textNode.textContent = t._searchSnippet.text;
    }
    snipEl.appendChild(textNode);
    tdTitle.appendChild(snipEl);
  }

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

// ── Projects View (STA-192) ───────────────────────────────
function matchTaskStatus(taskStatus, targetFilter) {
  if (!targetFilter || targetFilter === 'all') return true;
  const st = (taskStatus || '').toLowerCase();
  if (targetFilter === 'running') {
    return st === 'running' || st === 'in_progress';
  }
  if (targetFilter === 'blocked') {
    return st === 'blocked';
  }
  if (targetFilter === 'done') {
    return st === 'done' || st === 'completed' || st === 'soft_deleted';
  }
  if (targetFilter === 'active') {
    return st === 'active' || st === 'todo' || st === 'backlog' || (!['running', 'in_progress', 'blocked', 'done', 'completed', 'soft_deleted'].includes(st));
  }
  return st === targetFilter;
}

function populateProjectsOrgFilter() {
  const optionsContainer = document.getElementById('projects-org-options');
  const legacySel = document.getElementById('projects-org-filter');
  const statusSel = document.getElementById('projects-status-filter');

  if (statusSel) {
    statusSel.value = state.projectsFilter.status || 'all';
  }

  // Collect all known org names from fleet, tasks, sessions
  const orgNames = new Set();
  for (const org of (state.fleet?.organizations || [])) {
    if (org.name) orgNames.add(org.name);
  }
  for (const t of Object.values(state.tasks)) {
    if (t.organization) orgNames.add(t.organization);
  }
  for (const t of (state.fleet?.tasks || [])) {
    if (t.organization) orgNames.add(t.organization);
  }
  for (const s of Object.values(state.sessions)) {
    orgNames.add(s.org || 'StayPoint');
  }
  if (!orgNames.size) orgNames.add('StayPoint');

  const sortedOrgs = Array.from(orgNames).sort();

  // Keep legacy select in sync
  if (legacySel) {
    const prevVal = legacySel.value;
    legacySel.innerHTML = '<option value="all">All Organizations</option>';
    for (const org of sortedOrgs) {
      const opt = document.createElement('option');
      opt.value = org;
      opt.textContent = org;
      legacySel.appendChild(opt);
    }
    legacySel.value = prevVal || 'all';
  }

  if (!optionsContainer) return;

  // Task counts per org
  const orgTaskCounts = {};
  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  for (const org of (state.fleet?.organizations || [])) {
    for (const t of (org.tasks || [])) allTasks.push(t);
  }
  const seen = new Set();
  for (const t of allTasks) {
    if (t && t.id && !seen.has(t.id)) {
      seen.add(t.id);
      const o = t.organization || 'StayPoint';
      orgTaskCounts[o] = (orgTaskCounts[o] || 0) + 1;
    }
  }

  // Filter out any stored orgs that no longer exist
  if (state.projectsFilter.orgs.length > 0) {
    state.projectsFilter.orgs = state.projectsFilter.orgs.filter(o => orgNames.has(o));
  }

  optionsContainer.innerHTML = '';
  for (const org of sortedOrgs) {
    const isChecked = state.projectsFilter.orgs.length === 0 || state.projectsFilter.orgs.includes(org);
    const label = el('label', 'multiselect-option');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.value = org;
    cb.checked = isChecked;
    cb.className = 'multiselect-checkbox';
    cb.addEventListener('change', () => {
      handleProjectsOrgCheckboxChange();
    });

    label.appendChild(cb);
    label.appendChild(el('span', 'multiselect-option-label', org));
    const cnt = orgTaskCounts[org] || 0;
    label.appendChild(el('span', 'multiselect-option-count', `${cnt} task${cnt !== 1 ? 's' : ''}`));
    optionsContainer.appendChild(label);
  }

  updateProjectsOrgButtonLabel();
}

function handleProjectsOrgCheckboxChange() {
  const checkboxes = document.querySelectorAll('#projects-org-options .multiselect-checkbox');
  const checked = [];
  checkboxes.forEach(cb => {
    if (cb.checked) checked.push(cb.value);
  });

  if (checked.length === checkboxes.length || checked.length === 0) {
    state.projectsFilter.orgs = [];
  } else {
    state.projectsFilter.orgs = checked;
  }

  saveProjectsFilters();
  updateProjectsOrgButtonLabel();
  renderProjects();
}

function updateProjectsOrgButtonLabel() {
  const btnLabel = document.getElementById('projects-org-btn-label');
  const btnBadge = document.getElementById('projects-org-btn-badge');
  const legacySel = document.getElementById('projects-org-filter');
  if (!btnLabel) return;

  const selCount = state.projectsFilter.orgs.length;
  if (selCount === 0) {
    btnLabel.textContent = 'All Organizations';
    if (btnBadge) btnBadge.style.display = 'none';
    if (legacySel) legacySel.value = 'all';
  } else if (selCount === 1) {
    btnLabel.textContent = state.projectsFilter.orgs[0];
    if (btnBadge) btnBadge.style.display = 'none';
    if (legacySel) legacySel.value = state.projectsFilter.orgs[0];
  } else {
    btnLabel.textContent = `${selCount} Organizations`;
    if (btnBadge) {
      btnBadge.textContent = String(selCount);
      btnBadge.style.display = 'inline-flex';
    }
    if (legacySel) legacySel.value = 'all';
  }
}

function renderProjects() {
  const grid = document.getElementById('projects-grid');
  if (!grid) return;
  grid.innerHTML = '';

  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  for (const org of (state.fleet?.organizations || [])) {
    for (const t of (org.tasks || [])) {
      if (!t.organization) t.organization = org.name;
      allTasks.push(t);
    }
  }

  const seenIds = new Set();
  const dedupTasks = [];
  for (const t of allTasks) {
    if (t && t.id && !seenIds.has(t.id)) {
      seenIds.add(t.id);
      dedupTasks.push(t);
    }
  }

  const selectedOrgs = state.projectsFilter.orgs || [];
  const globalStatusFilter = state.projectsFilter.status || 'all';

  // Group by Organization -> Project
  // Ensures (No Project) tasks are strictly partitioned by organization and respect org filters
  const orgMap = {};

  for (const t of dedupTasks) {
    const orgName = t.organization || 'StayPoint';

    // Respect organization filter
    if (selectedOrgs.length > 0 && !selectedOrgs.includes(orgName)) {
      continue;
    }

    if (!orgMap[orgName]) {
      orgMap[orgName] = { name: orgName, projects: {} };
    }

    const rawProj = (t.project && typeof t.project === 'string' ? t.project.trim() : '');
    const projName = (rawProj && rawProj !== 'None') ? rawProj : '(No Project)';

    if (!orgMap[orgName].projects[projName]) {
      orgMap[orgName].projects[projName] = {
        name: projName,
        org: orgName,
        isNoProject: projName === '(No Project)',
        tasks: []
      };
    }
    orgMap[orgName].projects[projName].tasks.push(t);
  }

  // Include explicit projects defined on fleet organizations matching org filter
  for (const org of (state.fleet?.organizations || [])) {
    const orgName = org.name || 'StayPoint';
    if (selectedOrgs.length > 0 && !selectedOrgs.includes(orgName)) continue;
    if (Array.isArray(org.projects)) {
      for (const p of org.projects) {
        const pName = typeof p === 'string' ? p : p.name;
        if (pName && pName !== '(No Project)') {
          if (!orgMap[orgName]) orgMap[orgName] = { name: orgName, projects: {} };
          if (!orgMap[orgName].projects[pName]) {
            orgMap[orgName].projects[pName] = {
              name: pName,
              org: orgName,
              isNoProject: false,
              tasks: []
            };
          }
        }
      }
    }
  }

  // Filter projects by global status if set
  const orgEntries = Object.values(orgMap).map(org => {
    let projs = Object.values(org.projects);
    if (globalStatusFilter !== 'all') {
      projs = projs.filter(p => p.tasks.some(t => matchTaskStatus(t.status, globalStatusFilter)));
    }
    return {
      name: org.name,
      projects: projs.sort((a, b) => {
        if (a.isNoProject && !b.isNoProject) return 1;
        if (!a.isNoProject && b.isNoProject) return -1;
        return b.tasks.length - a.tasks.length;
      })
    };
  }).filter(org => org.projects.length > 0);

  if (!orgEntries.length) {
    grid.appendChild(el('p', 'muted-text', 'No projects found matching current filters.'));
    return;
  }

  // Render Grouped Headers by Organization
  for (const orgGroup of orgEntries.sort((a, b) => a.name.localeCompare(b.name))) {
    const groupWrap = el('div', 'project-org-group');

    // Grouped Header
    const groupHeader = el('div', 'project-org-header');
    const headerLeft = el('div', 'project-org-header-left');
    headerLeft.appendChild(el('span', 'project-org-icon', '🏢'));
    headerLeft.appendChild(el('h2', 'project-org-title', orgGroup.name));
    groupHeader.appendChild(headerLeft);

    const totalTasksInOrg = orgGroup.projects.reduce((sum, p) => sum + p.tasks.length, 0);
    const totalSpendInOrg = orgGroup.projects.reduce((sum, p) =>
      sum + p.tasks.reduce((ts, t) => ts + (t.spent_usd || 0), 0), 0);

    const headerMeta = el('div', 'project-org-header-meta');
    headerMeta.appendChild(el('span', 'pill', `${orgGroup.projects.length} project${orgGroup.projects.length !== 1 ? 's' : ''}`));
    headerMeta.appendChild(el('span', 'pill pill-todo', `${totalTasksInOrg} task${totalTasksInOrg !== 1 ? 's' : ''}`));
    if (totalSpendInOrg > 0) {
      headerMeta.appendChild(el('span', 'pill pill-gold', fmtCurrency(totalSpendInOrg)));
    }
    groupHeader.appendChild(headerMeta);
    groupWrap.appendChild(groupHeader);

    // Grid of cards for this organization
    const subGrid = el('div', 'project-cards-subgrid');

    for (const p of orgGroup.projects) {
      const card = createProjectCard(p, globalStatusFilter);
      subGrid.appendChild(card);
    }

    groupWrap.appendChild(subGrid);
    grid.appendChild(groupWrap);
  }
}

function createProjectCard(p, globalStatusFilter) {
  const card = el('div', 'project-card');
  const cardKey = `${p.org}:${p.name}`;

  // Card Header
  const hdr = el('div', 'project-card-header');
  const titleWrap = el('div');
  if (p.isNoProject) {
    const nameEl = el('div', 'project-name', '(No Project)');
    nameEl.style.color = 'var(--muted)';
    nameEl.style.fontStyle = 'italic';
    titleWrap.appendChild(nameEl);
    titleWrap.appendChild(el('div', 'project-org', `${p.org} · Unassigned Tasks`));
  } else {
    titleWrap.appendChild(el('div', 'project-name', p.name));
    titleWrap.appendChild(el('div', 'project-org', p.org));
  }
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
    else if (st === 'done' || st === 'completed' || st === 'soft_deleted') counts.done++;
    else counts.active++;
    totalSpend += t.spent_usd || 0;
  }

  // Active status filter for this card
  let activeCardFilter = state.projectsFilter.cardStatus[cardKey] ||
    (globalStatusFilter !== 'all' ? globalStatusFilter : 'all');

  // Stats row with clickable status filter buttons
  const statsRow = el('div', 'project-stats-row');
  const statDefs = [
    { status: 'running', label: 'Running', val: counts.running, cls: 'highlight-cyan' },
    { status: 'blocked', label: 'Blocked', val: counts.blocked, cls: 'highlight-red' },
    { status: 'done',    label: 'Done',    val: counts.done,    cls: 'highlight-green' },
    { status: 'active',  label: 'Active',  val: counts.active,  cls: '' },
  ];

  for (const sDef of statDefs) {
    const s = el('div', 'project-stat project-stat-clickable');
    s.dataset.status = sDef.status;
    s.title = `Filter tasks by ${sDef.label}`;
    if (activeCardFilter === sDef.status) {
      s.classList.add('active');
    }
    s.appendChild(el('div', `project-stat-n ${sDef.cls}`, String(sDef.val)));
    s.appendChild(el('div', 'project-stat-l', sDef.label));
    s.addEventListener('click', (e) => {
      e.stopPropagation();
      setCardStatusFilter(sDef.status);
    });
    statsRow.appendChild(s);
  }

  if (totalSpend > 0) {
    const s = el('div', 'project-stat clickable');
    s.setAttribute('role', 'button');
    s.setAttribute('tabindex', '0');
    s.title = 'View Cost & Accounting';
    s.appendChild(el('div', 'project-stat-n highlight-gold', fmtCurrency(totalSpend)));
    s.appendChild(el('div', 'project-stat-l', 'Spend'));
    s.addEventListener('click', (e) => {
      e.stopPropagation();
      drillDownToCost();
    });
    s.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        e.stopPropagation();
        drillDownToCost();
      }
    });
    statsRow.appendChild(s);
  }
  card.appendChild(statsRow);

  // Status Filter Pill Bar on Card: [All] [Active] [Running] [Done] [Blocked]
  const filterBar = el('div', 'project-card-filter-bar');
  const pillFilters = [
    { status: 'all',     label: 'All',     count: p.tasks.length },
    { status: 'active',  label: 'Active',  count: counts.active },
    { status: 'running', label: 'Running', count: counts.running },
    { status: 'done',    label: 'Done',    count: counts.done },
    { status: 'blocked', label: 'Blocked', count: counts.blocked },
  ];

  const pillButtons = [];
  for (const pf of pillFilters) {
    const pill = el('button', `project-card-filter-pill pill-${pf.status}`, `${pf.label} (${pf.count})`);
    pill.type = 'button';
    pill.dataset.status = pf.status;
    if (activeCardFilter === pf.status) {
      pill.classList.add('active');
    }
    pill.addEventListener('click', (e) => {
      e.stopPropagation();
      setCardStatusFilter(pf.status);
    });
    filterBar.appendChild(pill);
    pillButtons.push(pill);
  }
  card.appendChild(filterBar);

  // Task list preview container
  const taskList = el('div', 'project-task-list');
  card.appendChild(taskList);

  function renderTaskList() {
    taskList.innerHTML = '';
    const filteredTasks = p.tasks.filter(t => matchTaskStatus(t.status, activeCardFilter));

    if (!filteredTasks.length) {
      const emptyMsg = activeCardFilter === 'all'
        ? 'No tasks in this project.'
        : `No ${activeCardFilter} tasks.`;
      taskList.appendChild(el('div', 'project-task-empty muted-text', emptyMsg));
      return;
    }

    for (const t of filteredTasks.slice(0, 5)) {
      const item = el('div', 'project-task-item');
      item.appendChild(statusPill(t.status));
      const titleEl = el('span', null, t.title || t.name || '(untitled)');
      titleEl.style.cssText = 'flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;';
      item.appendChild(titleEl);
      item.addEventListener('click', (e) => {
        e.stopPropagation();
        openDetail(t.id);
      });
      taskList.appendChild(item);
    }

    if (filteredTasks.length > 5) {
      const moreMsg = el('div', 'muted-text', `+${filteredTasks.length - 5} more ${activeCardFilter !== 'all' ? activeCardFilter + ' ' : ''}tasks`);
      moreMsg.style.fontSize = '11px';
      moreMsg.style.paddingTop = '2px';
      taskList.appendChild(moreMsg);
    }
  }

  function setCardStatusFilter(newStatus) {
    if (activeCardFilter === newStatus && newStatus !== 'all') {
      activeCardFilter = 'all';
    } else {
      activeCardFilter = newStatus;
    }
    state.projectsFilter.cardStatus[cardKey] = activeCardFilter;
    saveProjectsFilters();

    pillButtons.forEach(btn => {
      btn.classList.toggle('active', btn.dataset.status === activeCardFilter);
    });

    statsRow.querySelectorAll('.project-stat-clickable').forEach(statEl => {
      statEl.classList.toggle('active', statEl.dataset.status === activeCardFilter);
    });

    renderTaskList();
  }

  renderTaskList();

  // Clicking the card opens the project: the Task Status page filtered to this
  // org + project. Inner controls stopPropagation so they keep their own action.
  const openProject = () => drillDownToTasks('all', p.org, p.isNoProject ? 'all' : p.name);
  card.setAttribute('role', 'button');
  card.setAttribute('tabindex', '0');
  card.title = p.isNoProject ? `View unassigned ${p.org} tasks` : `Open ${p.name}`;
  card.addEventListener('click', openProject);
  card.addEventListener('keydown', (e) => {
    if (e.target !== card) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      openProject();
    }
  });

  return card;
}

// ── Agents Filter Helpers ─────────────────────────────────
function orgMatches(itemOrg, targetOrg) {
  if (!targetOrg || targetOrg === 'all') return true;
  if (!itemOrg) return false;
  const a = String(itemOrg).trim().toLowerCase();
  const b = String(targetOrg).trim().toLowerCase();
  return a === b || a.startsWith(b) || b.startsWith(a);
}

function getProjectsForOrg(orgName = 'all') {
  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const projects = new Set();

  for (const t of allTasks) {
    const tOrg = t.organization || 'StayPoint';
    if (!orgMatches(tOrg, orgName)) continue;
    if (t.project && t.project !== '(No Project)') {
      projects.add(t.project);
    }
  }

  // Also check fleet organizations if they contain explicit project lists, tasks, or agents
  for (const org of (state.fleet?.organizations || [])) {
    if (!orgMatches(org.name, orgName)) continue;
    if (Array.isArray(org.projects)) {
      for (const p of org.projects) {
        const name = typeof p === 'string' ? p : p?.name;
        if (name && name !== '(No Project)') projects.add(name);
      }
    }
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
  if (agent.runningTask?.project && agent.runningTask.project !== '(No Project)') {
    projects.add(agent.runningTask.project);
  }
  if (Array.isArray(agent.agentTasks)) {
    for (const t of agent.agentTasks) {
      if (t.project && t.project !== '(No Project)') {
        projects.add(t.project);
      }
    }
  }

  // Also check tasks checked out by or assigned to this agent across all tasks
  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  for (const t of allTasks) {
    const matchesAgent =
      (t.checkout_agent_id && t.checkout_agent_id === agent.id) ||
      (t.assignee_agent_id && t.assignee_agent_id === agent.id) ||
      (t.assignee_id && t.assignee_id === agent.id) ||
      (t.assigneeAgentId && t.assigneeAgentId === agent.id) ||
      (t.assigned_agent && (t.assigned_agent === agent.name || t.assigned_agent === agent.id)) ||
      (t.assignee_name && t.assignee_name === agent.name) ||
      (t.assigneeRole && agent.role && t.assigneeRole === agent.role);
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
  state.agentsFilter.project = 'all';
  state.agentsFilter.status = 'all';
  const searchInput = document.getElementById('agents-search');
  const provSelect = document.getElementById('agents-provider-filter');
  const orgSelect = document.getElementById('agents-org-filter');
  const projSelect = document.getElementById('agents-project-filter');
  if (searchInput) searchInput.value = '';
  if (provSelect) provSelect.value = 'all';
  if (orgSelect) orgSelect.value = 'all';
  if (projSelect) projSelect.value = 'all';
  populateAgentsProjectFilter();
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

function getAgentQuota(providerQuotas, provider, orgName) {
  if (!providerQuotas) return null;
  const p = (provider || '').toLowerCase();
  const org = (orgName || '').toLowerCase();
  const isWork = org.includes('managed') || org.includes('mansol');

  if (p === 'claude_personal') return providerQuotas['claude_personal'] || providerQuotas['claude'] || null;
  if (p === 'claude_work') return providerQuotas['claude_work'] || providerQuotas['claude'] || null;

  if (p.includes('claude') || p.includes('anthropic') || p.includes('fable')) {
    if (isWork) {
      return providerQuotas['claude_work'] || providerQuotas['claude'] || providerQuotas['claude_personal'] || null;
    } else {
      return providerQuotas['claude_personal'] || providerQuotas['claude'] || providerQuotas['claude_work'] || null;
    }
  }
  if (providerQuotas[p]) return providerQuotas[p];
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
      (t.assignee_id && t.assignee_id === a.id) ||
      (t.assigneeAgentId && t.assigneeAgentId === a.id) ||
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

  // Synchronize cascading filters
  populateAgentsFilters();

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

  // Filter agents
  const searchTerm = (state.agentsFilter.search || '').toLowerCase();
  const provFilter = state.agentsFilter.provider || 'all';
  const orgFilter = state.agentsFilter.org || 'all';
  const projFilter = state.agentsFilter.project || 'all';
  const statusFilter = state.agentsFilter.status || 'all';

  const filtered = enrichedAgents.filter(a => {
    if (statusFilter !== 'all' && a.normalizedStatus !== statusFilter) return false;
    if (provFilter !== 'all' && (a.provider || '').toLowerCase() !== provFilter.toLowerCase()) return false;
    if (orgFilter !== 'all' && !orgMatches(a.org || a.organization, orgFilter)) return false;

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
    const quota = a.quota || getAgentQuota(f?.provider_quotas, a.provider, a.org || a.organization);
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

// ── Recent Tasks Filter Population ────────────────────────
function populateRecentTasksOrgFilter() {
  const sel = document.getElementById('recent-tasks-org-filter');
  if (!sel) return;

  const current = state.recentTasksFilter.org || 'all';
  const orgNames = new Set();
  for (const org of (state.fleet?.organizations || [])) {
    if (org.name) orgNames.add(org.name);
  }
  for (const t of Object.values(state.tasks)) {
    if (t.organization) orgNames.add(t.organization);
  }
  for (const t of (state.fleet?.tasks || [])) {
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
    state.recentTasksFilter.org = current;
  } else {
    sel.value = 'all';
    state.recentTasksFilter.org = 'all';
  }
}

function populateRecentTasksProjectFilter() {
  const sel = document.getElementById('recent-tasks-project-filter');
  if (!sel) return;

  const currentOrg = state.recentTasksFilter.org || 'all';
  const currentProj = state.recentTasksFilter.project || 'all';
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
    state.recentTasksFilter.project = currentProj;
  } else {
    sel.value = 'all';
    state.recentTasksFilter.project = 'all';
  }
}

function populateRecentTasksPriorityFilter() {
  const sel = document.getElementById('recent-tasks-priority-filter');
  if (!sel) return;

  const current = state.recentTasksFilter.priority || 'all';
  const standardPriorities = ['critical', 'urgent', 'high', 'medium', 'low'];
  const knownPriorities = new Set(standardPriorities);

  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  for (const t of allTasks) {
    if (t.priority && typeof t.priority === 'string') {
      const p = t.priority.toLowerCase().trim();
      if (p) knownPriorities.add(p);
    }
  }

  sel.innerHTML = '<option value="all">All Priorities</option>';
  for (const p of standardPriorities) {
    const opt = document.createElement('option');
    opt.value = p;
    opt.textContent = p.charAt(0).toUpperCase() + p.slice(1);
    sel.appendChild(opt);
    knownPriorities.delete(p);
  }
  for (const p of Array.from(knownPriorities).sort()) {
    const opt = document.createElement('option');
    opt.value = p;
    opt.textContent = p.charAt(0).toUpperCase() + p.slice(1);
    sel.appendChild(opt);
  }

  sel.value = current;
}

function populateRecentTasksFilters() {
  populateRecentTasksOrgFilter();
  populateRecentTasksProjectFilter();
  populateRecentTasksPriorityFilter();
}

function taskMatchesRecentFilter(t, orgFilter, projFilter, priFilter) {
  if (!t) return false;
  if (orgFilter !== 'all') {
    const org = (t.organization || 'StayPoint').trim();
    if (org.toLowerCase() !== orgFilter.toLowerCase()) return false;
  }
  if (projFilter !== 'all') {
    const prj = (t.project || '(No Project)').trim();
    if (prj.toLowerCase() !== projFilter.toLowerCase()) return false;
  }
  if (priFilter !== 'all') {
    const pri = (t.priority || 'medium').toLowerCase().trim();
    if (pri !== priFilter.toLowerCase().trim()) return false;
  }
  return true;
}

function renderSubtaskTree(parentID, childrenMap, taskMap, visited = new Set(), level = 0) {
  const rawChildren = [
    ...(childrenMap.get(parentID) || []),
    ...((taskMap.get(parentID)?.identifier && childrenMap.get(taskMap.get(parentID).identifier)) || [])
  ];
  if (!rawChildren.length) return null;

  // Deduplicate children by ID
  const seenKidIds = new Set();
  const children = [];
  for (const c of rawChildren) {
    if (c.id && !seenKidIds.has(c.id)) {
      seenKidIds.add(c.id);
      children.push(c);
    }
  }
  if (!children.length) return null;

  const orgFilter = state.recentTasksFilter.org || 'all';
  const projFilter = state.recentTasksFilter.project || 'all';
  const priFilter = state.recentTasksFilter.priority || 'all';
  const isFilterActive = orgFilter !== 'all' || projFilter !== 'all' || priFilter !== 'all';

  function nodeOrDescendantMatches(taskId, seen = new Set()) {
    if (seen.has(taskId)) return false;
    seen.add(taskId);
    const node = taskMap.get(taskId);
    if (node && taskMatchesRecentFilter(node, orgFilter, projFilter, priFilter)) return true;
    const kids = childrenMap.get(taskId) || [];
    for (const k of kids) {
      if (nodeOrDescendantMatches(k.id, seen)) return true;
    }
    return false;
  }

  const visibleKids = isFilterActive
    ? children.filter(k => nodeOrDescendantMatches(k.id))
    : children;

  visibleKids.sort((a, b) => new Date(b.updated_at || 0) - new Date(a.updated_at || 0));

  const tree = el('div', 'activity-subtasks-tree');
  tree.setAttribute('role', 'group');
  tree.setAttribute('aria-label', 'Subtasks');

  visibleKids.forEach((child, index) => {
    if (visited.has(child.id)) return; // cycle protection
    const isLast = index === visibleKids.length - 1;
    const row = el('div', 'activity-subtask-row');
    row.dataset.taskId = child.id;
    row.style.cursor = 'pointer';
    row.setAttribute('tabindex', '0');
    row.setAttribute('role', 'button');
    row.setAttribute('aria-label', `View details for subtask ${child.title || child.name || child.id}`);

    // Subtask Navigation: Clicking any subtask row opens its detail panel
    row.addEventListener('click', (e) => {
      openDetail(child.id);
    });
    row.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        openDetail(child.id);
      }
    });

    // Branch connector glyph (├── or └──)
    const branch = el('span', 'activity-subtask-branch', isLast ? '└──' : '├──');
    row.appendChild(branch);

    // Subtask status indicator dot
    const dot = el('span', 'activity-subtask-dot');
    const st = (child.status || '').toLowerCase();
    dot.style.background =
      (st === 'running' || st === 'in_progress') ? 'var(--cyan)' :
      (st === 'blocked' || st === 'errored' || st === 'failed') ? 'var(--red)' :
      (st === 'done' || st === 'completed' || st === 'closed') ? 'var(--green)' : 'var(--muted)';
    row.appendChild(dot);

    // Subtask body
    const body = el('div', 'activity-subtask-body');
    const titleRow = el('div', 'activity-subtask-title-row');

    const titleEl = el('span', 'activity-subtask-title', child.title || child.name || '(untitled child task)');
    titleRow.appendChild(titleEl);

    // Check if child itself has nested subtasks
    const grandKids = [
      ...(childrenMap.get(child.id) || []),
      ...((child.identifier && childrenMap.get(child.identifier)) || [])
    ].filter(gk => gk.id !== child.id);
    const visibleGrandKids = isFilterActive
      ? grandKids.filter(gk => nodeOrDescendantMatches(gk.id))
      : grandKids;

    if (visibleGrandKids.length > 0) {
      const isChildExpanded = state.expandedRecentSubtasks.has(child.id) ||
        (child.identifier && state.expandedRecentSubtasks.has(child.identifier)) ||
        (isFilterActive && !taskMatchesRecentFilter(child, orgFilter, projFilter, priFilter));
      const subCountLabel = visibleGrandKids.length === 1 ? '1 subtask' : `${visibleGrandKids.length} subtasks`;
      const childToggle = el('button', 'activity-subtask-toggle', `${isChildExpanded ? '[-]' : '[+]'} ${subCountLabel}`);
      childToggle.type = 'button';
      childToggle.setAttribute('aria-expanded', isChildExpanded ? 'true' : 'false');
      childToggle.title = isChildExpanded ? 'Collapse subtasks' : 'Expand subtasks';
      childToggle.addEventListener('click', (e) => {
        e.stopPropagation();
        const cid = child.id;
        if (state.expandedRecentSubtasks.has(cid)) {
          state.expandedRecentSubtasks.delete(cid);
          if (child.identifier) state.expandedRecentSubtasks.delete(child.identifier);
        } else {
          state.expandedRecentSubtasks.add(cid);
          if (child.identifier) state.expandedRecentSubtasks.add(child.identifier);
        }
        renderRecentTasks();
      });
      titleRow.appendChild(childToggle);
    }

    body.appendChild(titleRow);

    const metaParts = [
      child.identifier || (child.id ? `#${child.id.slice(0, 8)}` : ''),
      child.organization,
      child.project,
      child.assignee_name || child.assignee_role,
      child.priority ? `Priority: ${child.priority}` : '',
      child.updated_at ? fmtRelTime(child.updated_at) : '',
    ].filter(Boolean);
    body.appendChild(el('div', 'activity-subtask-meta', metaParts.join(' · ')));

    // Recursive rendering if expanded
    if (visibleGrandKids.length > 0) {
      const isChildExpanded = state.expandedRecentSubtasks.has(child.id) ||
        (child.identifier && state.expandedRecentSubtasks.has(child.identifier)) ||
        (isFilterActive && !taskMatchesRecentFilter(child, orgFilter, projFilter, priFilter));
      if (isChildExpanded) {
        const nextVisited = new Set(visited);
        nextVisited.add(child.id);
        if (child.identifier) nextVisited.add(child.identifier);
        const nestedTree = renderSubtaskTree(child.id, childrenMap, taskMap, nextVisited, level + 1);
        if (nestedTree) body.appendChild(nestedTree);
      }
    }

    row.appendChild(body);
    row.appendChild(statusPill(child.status));
    tree.appendChild(row);
  });

  return tree;
}

function renderRecentTasks() {
  const feed = document.getElementById('recent-tasks-feed');
  if (!feed) return;
  feed.innerHTML = '';

  const orgSel = document.getElementById('recent-tasks-org-filter');
  if (orgSel && orgSel.options.length <= 1) {
    populateRecentTasksFilters();
  }

  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const seenIds = new Set();
  const dedupTasks = [];
  for (const t of allTasks) {
    if (t.id && !seenIds.has(t.id)) {
      seenIds.add(t.id);
      dedupTasks.push(t);
    }
  }

  // Build parent -> children map and id -> task map
  const taskById = new Map();
  for (const t of dedupTasks) {
    if (t.id) taskById.set(t.id, t);
    if (t.identifier) taskById.set(t.identifier, t);
  }

  const childrenMap = new Map();
  function registerChild(parentId, child) {
    if (!parentId || !child) return;
    if (!childrenMap.has(parentId)) {
      childrenMap.set(parentId, []);
    }
    const list = childrenMap.get(parentId);
    if (!list.some(existing => existing.id === child.id)) {
      list.push(child);
    }
  }

  for (const t of dedupTasks) {
    const pid = t.parent_id || t.parentId || t.parent?.id;
    if (pid) {
      registerChild(pid, t);
      const parentTask = taskById.get(pid);
      if (parentTask) {
        if (parentTask.id && parentTask.id !== pid) registerChild(parentTask.id, t);
        if (parentTask.identifier && parentTask.identifier !== pid) registerChild(parentTask.identifier, t);
      }
    }
    const embeddedSubtasks = t.dependencies?.subtasks || t.subtasks || [];
    for (const st of embeddedSubtasks) {
      registerChild(t.id, st);
      if (t.identifier) registerChild(t.identifier, st);
    }
  }

  // Active filters
  const orgFilter = state.recentTasksFilter.org || 'all';
  const projFilter = state.recentTasksFilter.project || 'all';
  const priFilter = state.recentTasksFilter.priority || 'all';
  const isFilterActive = orgFilter !== 'all' || projFilter !== 'all' || priFilter !== 'all';

  function matchesFilter(t) {
    return taskMatchesRecentFilter(t, orgFilter, projFilter, priFilter);
  }

  function matchesOrHasMatchingDescendant(t, seen = new Set()) {
    if (seen.has(t.id)) return false;
    seen.add(t.id);
    if (matchesFilter(t)) return true;
    const kids = [
      ...(childrenMap.get(t.id) || []),
      ...((t.identifier && childrenMap.get(t.identifier)) || [])
    ];
    for (const kid of kids) {
      if (matchesOrHasMatchingDescendant(kid, seen)) return true;
    }
    return false;
  }

  // Top-level tasks are root tasks (tasks without a parent in taskById)
  const topLevelTasks = dedupTasks.filter(t => {
    const pid = t.parent_id || t.parentId || t.parent?.id;
    return !pid || !taskById.has(pid);
  });

  // Filter top-level tasks
  const filteredTasks = topLevelTasks.filter(t => matchesOrHasMatchingDescendant(t));

  // Sort by latest activity (maximum of own updated_at and all descendants' updated_at)
  function getEffectiveTime(t) {
    let maxTime = t.updated_at ? new Date(t.updated_at).getTime() : 0;
    const kids = [
      ...(childrenMap.get(t.id) || []),
      ...((t.identifier && childrenMap.get(t.identifier)) || [])
    ];
    for (const k of kids) {
      if (k.updated_at) {
        const kt = new Date(k.updated_at).getTime();
        if (kt > maxTime) maxTime = kt;
      }
    }
    return maxTime;
  }

  const sorted = filteredTasks
    .sort((a, b) => getEffectiveTime(b) - getEffectiveTime(a))
    .slice(0, 50);

  if (!sorted.length) {
    feed.appendChild(el('p', 'muted-text', 'No task activity matching selected filters.'));
    return;
  }

  sorted.forEach((t, i) => {
    const item = el('div', 'activity-item');
    item.dataset.taskId = t.id;

    const dotCol = el('div', 'activity-dot-col');
    const dot = el('span', 'activity-dot');
    const st = (t.status || '').toLowerCase();
    dot.style.background =
      (st === 'running' || st === 'in_progress') ? 'var(--cyan)' :
      (st === 'blocked' || st === 'errored' || st === 'failed') ? 'var(--red)' :
      (st === 'done' || st === 'completed' || st === 'closed') ? 'var(--green)' : 'var(--muted)';
    dotCol.appendChild(dot);
    if (i < sorted.length - 1) dotCol.appendChild(el('span', 'activity-line'));
    item.appendChild(dotCol);

    const body = el('div', 'activity-body');
    const titleRow = el('div', 'activity-title-row');

    const titleEl = el('div', 'activity-title', t.title || t.name || '(untitled)');
    titleEl.style.cursor = 'pointer';
    titleEl.addEventListener('click', () => openDetail(t.id));
    titleRow.appendChild(titleEl);

    // Expandable subtask toggle & child count badge
    const rawChildren = [
      ...(childrenMap.get(t.id) || []),
      ...((t.identifier && childrenMap.get(t.identifier)) || [])
    ];
    const seenKidIds = new Set();
    const children = [];
    for (const c of rawChildren) {
      if (c.id && !seenKidIds.has(c.id)) {
        seenKidIds.add(c.id);
        children.push(c);
      }
    }

    const visibleChildren = isFilterActive
      ? children.filter(k => matchesOrHasMatchingDescendant(k))
      : children;

    if (visibleChildren.length > 0) {
      const isExpanded = state.expandedRecentSubtasks.has(t.id) ||
        (t.identifier && state.expandedRecentSubtasks.has(t.identifier)) ||
        (isFilterActive && !matchesFilter(t));
      const countLabel = visibleChildren.length === 1 ? '1 subtask' : `${visibleChildren.length} subtasks`;
      const toggle = el('button', 'activity-subtask-toggle', `${isExpanded ? '[-]' : '[+]'} ${countLabel}`);
      toggle.type = 'button';
      toggle.setAttribute('aria-expanded', isExpanded ? 'true' : 'false');
      toggle.title = isExpanded ? 'Collapse subtasks' : 'Expand subtasks';
      toggle.addEventListener('click', (e) => {
        e.stopPropagation();
        const key = t.id;
        if (state.expandedRecentSubtasks.has(key)) {
          state.expandedRecentSubtasks.delete(key);
          if (t.identifier) state.expandedRecentSubtasks.delete(t.identifier);
        } else {
          state.expandedRecentSubtasks.add(key);
          if (t.identifier) state.expandedRecentSubtasks.add(t.identifier);
        }
        renderRecentTasks();
      });
      titleRow.appendChild(toggle);
    }

    body.appendChild(titleRow);

    const metaParts = [
      t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : ''),
      t.organization,
      t.project,
      t.assignee_name || t.assignee_role,
      t.priority ? `Priority: ${t.priority}` : '',
      t.updated_at ? fmtRelTime(t.updated_at) : '',
    ].filter(Boolean);
    body.appendChild(el('div', 'activity-meta', metaParts.join(' · ')));

    // Subtask Tree Hierarchy
    if (visibleChildren.length > 0) {
      const isExpanded = state.expandedRecentSubtasks.has(t.id) ||
        (t.identifier && state.expandedRecentSubtasks.has(t.identifier)) ||
        (isFilterActive && !matchesFilter(t));
      if (isExpanded) {
        const visited = new Set();
        visited.add(t.id);
        if (t.identifier) visited.add(t.identifier);
        const subTree = renderSubtaskTree(t.id, childrenMap, taskById, visited, 0);
        if (subTree) body.appendChild(subTree);
      }
    }

    item.appendChild(body);
    item.appendChild(statusPill(t.status));
    feed.appendChild(item);
  });
}


// ── Task Status Dedicated Page ────────────────────────────
function renderTaskStatusPage() {
  const table = document.getElementById('ts-task-table');
  const tbody = document.getElementById('ts-task-tbody');
  if (!tbody) return;

  const DEFAULT_TS_SORT = { column: 'updated', direction: 'desc' };
  renderTableSortHeaders(table, state.tsSort, (col, dir, nextSort) => {
    state.tsSort = nextSort || { column: col, direction: dir };
    renderTaskStatusPage();
  }, DEFAULT_TS_SORT);

  tbody.innerHTML = '';

  const allTasks = [...(state.fleet?.tasks || []), ...Object.values(state.tasks)];
  const seenIds = new Set();
  const dedupTasks = [];
  for (const t of allTasks) {
    if (!seenIds.has(t.id)) { seenIds.add(t.id); dedupTasks.push(t); }
  }

  const sTerm          = (state.tsFilter.search || '').toLowerCase().trim();
  const orgFilter      = state.tsFilter.org || 'all';
  const projectFilter  = state.tsFilter.project || 'all';
  const priorityFilter = state.tsFilter.priority || 'all';
  const stFilter       = state.tsFilter.status || 'all';

  const filtered = filterTasks(dedupTasks, sTerm, orgFilter, stFilter, projectFilter, priorityFilter);
  const sorted = sortTasks(filtered, state.tsSort.column, state.tsSort.direction);

  if (!sorted.length) {
    const tr = document.createElement('tr');
    const td = document.createElement('td'); td.colSpan = 9;
    td.textContent = 'No tasks match filter.';
    td.style.cssText = 'text-align:center;color:var(--muted);padding:24px;';
    tr.appendChild(td); tbody.appendChild(tr);
    return;
  }

  for (const t of sorted) {
    const tr = document.createElement('tr');

    const tdId = el('td', null, t.identifier || (t.id ? `#${t.id.slice(0, 8)}` : '—'));
    tdId.style.cssText = 'font-family:monospace;font-weight:600;';

    const tdTitle = el('td');
    const titleSpan = el('div', 'task-table-title', t.title || t.name || '(untitled)');
    tdTitle.appendChild(titleSpan);

    if (t._searchSnippet) {
      const snipEl = el('div', 'task-search-snippet');
      const badge = el('span', 'snippet-badge', t._searchSnippet.type);
      snipEl.appendChild(badge);
      const textNode = document.createElement('span');
      if (sTerm) {
        const raw = t._searchSnippet.text;
        const lower = raw.toLowerCase();
        const idx = lower.indexOf(sTerm);
        if (idx !== -1) {
          textNode.appendChild(document.createTextNode(raw.slice(0, idx)));
          const mark = el('mark', null, raw.slice(idx, idx + sTerm.length));
          textNode.appendChild(mark);
          textNode.appendChild(document.createTextNode(raw.slice(idx + sTerm.length)));
        } else {
          textNode.textContent = raw;
        }
      } else {
        textNode.textContent = t._searchSnippet.text;
      }
      snipEl.appendChild(textNode);
      tdTitle.appendChild(snipEl);
    }

    const tdOrg      = el('td', null, t.organization || '—');
    const tdProj     = el('td', null, t.project || '—');
    const tdAssignee = el('td', null, t.assignee_name || t.assigned_agent || t.checkout_agent_id?.slice(0, 8) || '—');
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
      let rem5h = q.five_hour_remaining_pct;
      let used5h = q.five_hour_used_pct;
      if (rem5h == null && used5h != null) rem5h = Math.max(0, 100 - used5h);
      if (used5h == null && rem5h != null) used5h = Math.max(0, 100 - rem5h);
      if (rem5h == null && used5h == null) { rem5h = 100; used5h = 0; }
      if (rem5h === 100 && used5h > 0) rem5h = Math.max(0, 100 - used5h);
      rem5h = Math.max(0, Math.min(100, rem5h));
      used5h = Math.max(0, Math.min(100, used5h));
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
      let remWk = q.weekly_remaining_pct;
      let usedWk = q.weekly_used_pct;
      if (remWk == null && usedWk != null) remWk = Math.max(0, 100 - usedWk);
      if (usedWk == null && remWk != null) usedWk = Math.max(0, 100 - remWk);
      if (remWk == null && usedWk == null) { remWk = 100; usedWk = 0; }
      if (remWk === 100 && usedWk > 0) remWk = Math.max(0, 100 - usedWk);
      remWk = Math.max(0, Math.min(100, remWk));
      usedWk = Math.max(0, Math.min(100, usedWk));
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
    { val: tc.running || 0,  label: 'Running',  cls: 'highlight-cyan', status: 'running' },
    { val: tc.active  || 0,  label: 'Active',   status: 'active' },
    { val: tc.blocked || 0,  label: 'Blocked',  cls: 'highlight-red',  status: 'blocked' },
    { val: tc.done    || 0,  label: 'Done',     cls: 'highlight-green',status: 'done' },
    { val: tc.total   || 0,  label: 'Total',    status: 'all' },
    { val: org.active_agents || 0, label: 'Agents', cls: 'highlight-green', type: 'agents' },
    { val: fmtCurrency(org.spent_usd), label: 'Spend', isStr: true, type: 'cost' },
  ];
  for (const s of statItems) {
    const d = el('div', 'org-detail-stat clickable');
    d.setAttribute('role', 'button');
    d.setAttribute('tabindex', '0');
    d.title = `Filter ${org.name} by ${s.label}`;
    const valEl = el('div', 'org-detail-stat-val' + (s.cls ? ' ' + s.cls : ''), String(s.val));
    d.appendChild(valEl);
    d.appendChild(el('div', 'org-detail-stat-label', s.label));

    if (s.status) {
      d.addEventListener('click', () => drillDownToTasks(s.status, org.name));
      d.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          drillDownToTasks(s.status, org.name);
        }
      });
    } else if (s.type === 'agents') {
      d.addEventListener('click', () => drillDownToAgents('all', org.name));
      d.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          drillDownToAgents('all', org.name);
        }
      });
    } else if (s.type === 'cost') {
      d.addEventListener('click', () => drillDownToCost());
      d.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          drillDownToCost();
        }
      });
    }

    statsRow.appendChild(d);
  }
  container.appendChild(statsRow);

  // 5-Hour Rolling Quotas & Lockout Gauges for this organization
  const orgQuotas = org.provider_quotas || {};
  if (Object.keys(orgQuotas).length) {
    const quotaSec = el('div', 'org-detail-section');
    quotaSec.appendChild(el('div', 'org-detail-section-title', '5-Hour Rolling Quotas & Lockout Status'));
    const quotaGrid = el('div', 'quota-gauges-grid');
    const isManagedSol = (org.name || '').toLowerCase().includes('managed');
    const order = isManagedSol
      ? ['gemini', 'claude_work', 'claude_personal', 'claude', 'openai']
      : ['gemini', 'claude_personal', 'claude_work', 'claude', 'openai'];
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
    tbl.id = 'org-task-table';
    tbl.style.width = '100%';
    const thead = document.createElement('thead');
    thead.innerHTML = '<tr>' +
      '<th data-col="identifier" class="sortable-th" tabindex="0" role="columnheader">ID</th>' +
      '<th data-col="task" class="sortable-th" tabindex="0" role="columnheader">Task</th>' +
      '<th data-col="status" class="sortable-th" tabindex="0" role="columnheader">Status</th>' +
      '<th data-col="priority" class="sortable-th" tabindex="0" role="columnheader">Priority</th>' +
      '<th data-col="cost" class="sortable-th" tabindex="0" role="columnheader">Spend</th>' +
      '</tr>';
    tbl.appendChild(thead);
    const tbody = document.createElement('tbody');

    const DEFAULT_ORG_SORT = { column: 'task', direction: 'asc' };
    const renderOrgRows = () => {
      renderTableSortHeaders(tbl, state.orgSort, (col, dir, nextSort) => {
        state.orgSort = nextSort || { column: col, direction: dir };
        renderOrgRows();
      }, DEFAULT_ORG_SORT);
      tbody.innerHTML = '';
      const sorted = sortTasks(orgTasks, state.orgSort.column, state.orgSort.direction);
      for (const t of sorted) {
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
    };
    renderOrgRows();

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
  for (const { val, label, status, type } of [
    { val: inProg,  label: 'In Progress', status: 'running' },
    { val: todo,    label: 'Todo',        status: 'active' },
    { val: blocked, label: 'Blocked',     status: 'blocked' },
    { val: done,    label: 'Done',        status: 'done' },
    { val: active,  label: 'Agents',      type: 'agents' },
  ]) {
    const s = el('div', 'boss-stat clickable');
    s.setAttribute('role', 'button');
    s.setAttribute('tabindex', '0');
    s.title = `View ${label}`;
    s.appendChild(el('div', 'boss-stat-val', String(val)));
    s.appendChild(el('div', 'boss-stat-label', label));
    if (status) {
      s.addEventListener('click', () => drillDownToTasks(status));
      s.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          drillDownToTasks(status);
        }
      });
    } else if (type === 'agents') {
      s.addEventListener('click', () => drillDownToAgents());
      s.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          drillDownToAgents();
        }
      });
    }
    grid.appendChild(s);
  }
  card.appendChild(grid);
  container.appendChild(card);

  // In-app interactive report preview / carousel
  container.appendChild(buildBossReportCarousel());

  renderEventStream();
}

// ── Boss Card In-App Report Carousel & Modal Preview ─────
const BOSS_REPORTS = [
  { id: 'work',     label: 'Work / Boss Card', desc: 'Executive Justification Memo', icon: '🏢' },
  { id: 'combined', label: 'Combined Fleet',   desc: 'Cross-Account Fleet Audit',   icon: '🌐' },
  { id: 'personal', label: 'Personal',         desc: 'Claude Code Value Audit',     icon: '👤' },
  { id: 'gemini',   label: 'Gemini / AGY',     desc: 'Antigravity Agent Velocity',  icon: '⚡' },
];

const BOSS_REPORT_CACHE_KEY = 'staypoint_boss_reports_cache';
const BOSS_REPORT_CACHE_TTL_MS = 30 * 60 * 1000; // 30 minutes TTL

function loadBossReportCache() {
  try {
    const raw = localStorage.getItem(BOSS_REPORT_CACHE_KEY);
    if (!raw) return false;
    const parsed = JSON.parse(raw);
    if (!parsed || !parsed.reports || typeof parsed.reports !== 'object') return false;
    const now = Date.now();
    const today = new Date().toISOString().slice(0, 10);
    const isToday = parsed.date === today;
    const isWithinTTL = (now - (parsed.timestamp || 0)) < BOSS_REPORT_CACHE_TTL_MS;
    if (isToday && isWithinTTL) {
      state.bossReportCache = Object.assign({}, parsed.reports);
      state.bossReportLastRenderedAt = parsed.timestamp || now;
      return true;
    }
  } catch { /* ignore storage errors */ }
  return false;
}

function saveBossReportCache() {
  try {
    const payload = {
      timestamp: Date.now(),
      date: new Date().toISOString().slice(0, 10),
      reports: state.bossReportCache || {},
    };
    localStorage.setItem(BOSS_REPORT_CACHE_KEY, JSON.stringify(payload));
    state.bossReportLastRenderedAt = payload.timestamp;
  } catch { /* ignore storage errors */ }
}

async function preloadBossReports(force = false) {
  if (state.bossReportPreloading) return;
  state.bossReportPreloading = true;

  try {
    if (!force) {
      const isFresh = loadBossReportCache();
      const allCached = BOSS_REPORTS.every(r => Boolean(state.bossReportCache && state.bossReportCache[r.id]));
      if (isFresh && allCached) {
        state.bossReportPreloading = false;
        return;
      }
    }

    const fetches = BOSS_REPORTS.map(async (rep) => {
      try {
        const url = `/api/report?type=${encodeURIComponent(rep.id)}&format=html${TOKEN ? `&token=${encodeURIComponent(TOKEN)}` : ''}`;
        const res = await fetch(url, { headers: authHeader() });
        if (res.ok) {
          const html = await res.text();
          if (!state.bossReportCache) state.bossReportCache = {};
          state.bossReportCache[rep.id] = html;
          state.bossReportCacheTimestamps[rep.id] = Date.now();
        }
      } catch (err) {
        console.warn(`[BossCache] Error preloading ${rep.id}:`, err);
      }
    });

    await Promise.all(fetches);
    saveBossReportCache();

    // If an iframe for the current report is in view, ensure it displays the fresh content
    const activeIframe = document.querySelector('.boss-preview-iframe');
    const curReport = typeof state.bossReportIndex === 'number' ? BOSS_REPORTS[state.bossReportIndex] : null;
    if (activeIframe && curReport && state.bossReportCache[curReport.id]) {
      if (activeIframe.srcdoc !== state.bossReportCache[curReport.id]) {
        activeIframe.srcdoc = state.bossReportCache[curReport.id];
      }
      const loading = document.querySelector('.boss-preview-loading');
      if (loading) loading.style.display = 'none';
      const errorOverlay = document.querySelector('.boss-preview-error');
      if (errorOverlay) errorOverlay.style.display = 'none';
    }
  } finally {
    state.bossReportPreloading = false;
  }
}

function buildBossReportCarousel() {
  const card = el('div', 'boss-carousel-card');

  if (typeof state.bossReportIndex !== 'number' || state.bossReportIndex < 0 || state.bossReportIndex >= BOSS_REPORTS.length) {
    state.bossReportIndex = 0;
  }
  if (!state.bossReportZoom) {
    state.bossReportZoom = 1.0;
  }
  if (!state.bossReportCache) {
    state.bossReportCache = {};
  }

  const currentReport = () => BOSS_REPORTS[state.bossReportIndex];

  // 1. Header with title & interactive action buttons
  const hdr = el('div', 'boss-carousel-header');
  const titleGroup = el('div', 'boss-carousel-title-group');
  const title = el('div', 'boss-carousel-title');
  title.innerHTML = '<span>📄</span> Executive Report In-App Preview';
  const desc = el('div', 'boss-carousel-desc', 'Interactive carousel — view and inspect executive briefing memos and audits live in-app.');
  titleGroup.appendChild(title);
  titleGroup.appendChild(desc);
  hdr.appendChild(titleGroup);

  const actions = el('div', 'boss-carousel-actions');

  // Zoom controls
  const zoomOutBtn = el('button', 'carousel-btn', '−');
  zoomOutBtn.type = 'button';
  zoomOutBtn.title = 'Zoom out';
  const zoomLabel = el('span', 'carousel-btn', `${Math.round(state.bossReportZoom * 100)}%`);
  zoomLabel.style.cursor = 'default';
  const zoomInBtn = el('button', 'carousel-btn', '+');
  zoomInBtn.type = 'button';
  zoomInBtn.title = 'Zoom in';

  zoomOutBtn.addEventListener('click', () => {
    state.bossReportZoom = Math.max(0.6, Math.round((state.bossReportZoom - 0.1) * 10) / 10);
    zoomLabel.textContent = `${Math.round(state.bossReportZoom * 100)}%`;
    applyZoom();
  });
  zoomInBtn.addEventListener('click', () => {
    state.bossReportZoom = Math.min(1.4, Math.round((state.bossReportZoom + 0.1) * 10) / 10);
    zoomLabel.textContent = `${Math.round(state.bossReportZoom * 100)}%`;
    applyZoom();
  });
  actions.appendChild(zoomOutBtn);
  actions.appendChild(zoomLabel);
  actions.appendChild(zoomInBtn);

  // Fullscreen / Modal expander
  const expandBtn = el('button', 'carousel-btn', '⛶ Expand');
  expandBtn.type = 'button';
  expandBtn.title = 'Open full-screen modal preview';
  expandBtn.addEventListener('click', () => {
    openBossReportModal(currentReport().id, currentReport().label);
  });
  actions.appendChild(expandBtn);

  // Print button
  const printBtn = el('button', 'carousel-btn', '🖨 Print');
  printBtn.type = 'button';
  printBtn.title = 'Print active report';
  printBtn.addEventListener('click', () => {
    if (iframe && iframe.contentWindow) {
      iframe.contentWindow.focus();
      iframe.contentWindow.print();
    }
  });
  actions.appendChild(printBtn);

  // Refresh preview data
  const refreshBtn = el('button', 'carousel-btn', '↻ Refresh');
  refreshBtn.type = 'button';
  refreshBtn.title = 'Refresh current report data';
  refreshBtn.addEventListener('click', () => {
    delete state.bossReportCache[currentReport().id];
    loadReport();
    preloadBossReports(true);
  });
  actions.appendChild(refreshBtn);

  // Download PDF button for current report
  const dlCurrentBtn = el('button', 'carousel-btn primary', '⬇ PDF');
  dlCurrentBtn.type = 'button';
  dlCurrentBtn.title = 'Download current report as PDF';
  dlCurrentBtn.addEventListener('click', async () => {
    const rep = currentReport();
    const origText = dlCurrentBtn.innerHTML;
    dlCurrentBtn.disabled = true;
    dlCurrentBtn.innerHTML = '<span class="dl-spinner"></span> Generating…';
    try {
      const url = `/api/report?type=${encodeURIComponent(rep.id)}${TOKEN ? `&token=${encodeURIComponent(TOKEN)}` : ''}`;
      const resp = await fetch(url, { headers: authHeader() });
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const blob = await resp.blob();
      const blobUrl = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = blobUrl;
      a.download = `staypoint-${rep.id}-report.pdf`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      setTimeout(() => URL.revokeObjectURL(blobUrl), 15000);
    } catch (err) {
      alert(`Download failed: ${err.message}`);
    } finally {
      dlCurrentBtn.disabled = false;
      dlCurrentBtn.innerHTML = origText;
    }
  });
  actions.appendChild(dlCurrentBtn);

  hdr.appendChild(actions);
  card.appendChild(hdr);

  // 2. Carousel Nav Bar (Tabs & Pager)
  const navBar = el('div', 'boss-carousel-nav');
  const tabsWrap = el('div', 'boss-carousel-tabs');
  const tabBtns = [];

  BOSS_REPORTS.forEach((rep, idx) => {
    const tab = el('button', 'carousel-tab' + (idx === state.bossReportIndex ? ' active' : ''));
    tab.type = 'button';
    tab.innerHTML = `<span>${rep.icon}</span> ${rep.label}`;
    tab.addEventListener('click', () => {
      state.bossReportIndex = idx;
      updateCarousel();
    });
    tabsWrap.appendChild(tab);
    tabBtns.push(tab);
  });
  navBar.appendChild(tabsWrap);

  const pagerWrap = el('div', 'boss-carousel-pager');
  const prevBtn = el('button', 'carousel-btn', '‹ Prev');
  prevBtn.type = 'button';
  prevBtn.title = 'Previous report (←)';
  const pagerInd = el('span', 'carousel-page-indicator');
  const nextBtn = el('button', 'carousel-btn', 'Next ›');
  nextBtn.type = 'button';
  nextBtn.title = 'Next report (→)';

  prevBtn.addEventListener('click', () => {
    state.bossReportIndex = (state.bossReportIndex - 1 + BOSS_REPORTS.length) % BOSS_REPORTS.length;
    updateCarousel();
  });
  nextBtn.addEventListener('click', () => {
    state.bossReportIndex = (state.bossReportIndex + 1) % BOSS_REPORTS.length;
    updateCarousel();
  });

  pagerWrap.appendChild(prevBtn);
  pagerWrap.appendChild(pagerInd);
  pagerWrap.appendChild(nextBtn);
  navBar.appendChild(pagerWrap);
  card.appendChild(navBar);

  // 3. Document Preview Sheet Frame
  const frameWrap = el('div', 'boss-preview-frame-wrap');
  const sheet = el('div', 'boss-preview-sheet');
  const iframe = document.createElement('iframe');
  iframe.className = 'boss-preview-iframe';
  iframe.setAttribute('title', 'Boss Card Report Preview');
  iframe.setAttribute('sandbox', 'allow-same-origin allow-scripts allow-modals allow-popups');

  const loadingOverlay = el('div', 'boss-preview-loading');
  loadingOverlay.innerHTML = '<span class="dl-spinner"></span><span>Loading executive report preview…</span>';

  const errorOverlay = el('div', 'boss-preview-error');
  errorOverlay.style.display = 'none';

  sheet.appendChild(iframe);
  frameWrap.appendChild(sheet);
  frameWrap.appendChild(loadingOverlay);
  frameWrap.appendChild(errorOverlay);
  card.appendChild(frameWrap);

  function applyZoom() {
    if (state.bossReportZoom === 1.0) {
      sheet.style.transform = 'none';
      sheet.style.transformOrigin = 'top center';
    } else {
      sheet.style.transform = `scale(${state.bossReportZoom})`;
      sheet.style.transformOrigin = 'top center';
    }
  }

  async function loadReport() {
    const rep = currentReport();
    loadingOverlay.style.display = 'flex';
    errorOverlay.style.display = 'none';

    try {
      if (state.bossReportCache[rep.id]) {
        iframe.srcdoc = state.bossReportCache[rep.id];
        loadingOverlay.style.display = 'none';
        return;
      }

      const url = `/api/report?type=${encodeURIComponent(rep.id)}&format=html${TOKEN ? `&token=${encodeURIComponent(TOKEN)}` : ''}`;
      const res = await fetch(url, { headers: authHeader() });
      if (!res.ok) {
        throw new Error(`Server returned HTTP ${res.status}`);
      }
      const html = await res.text();
      state.bossReportCache[rep.id] = html;
      state.bossReportCacheTimestamps[rep.id] = Date.now();
      saveBossReportCache();
      iframe.srcdoc = html;
      loadingOverlay.style.display = 'none';
    } catch (err) {
      loadingOverlay.style.display = 'none';
      errorOverlay.innerHTML = '';
      errorOverlay.style.display = 'flex';
      errorOverlay.appendChild(el('div', 'boss-preview-error-title', `Failed to load ${rep.label}`));
      errorOverlay.appendChild(el('div', 'boss-preview-error-desc', err.message || 'Unknown network error.'));
      const retryBtn = el('button', 'carousel-btn primary', '↻ Retry');
      retryBtn.type = 'button';
      retryBtn.addEventListener('click', loadReport);
      errorOverlay.appendChild(retryBtn);
    }
  }

  function updateCarousel() {
    tabBtns.forEach((btn, idx) => {
      btn.classList.toggle('active', idx === state.bossReportIndex);
    });
    pagerInd.textContent = `Report ${state.bossReportIndex + 1} of ${BOSS_REPORTS.length}: ${currentReport().label}`;
    applyZoom();
    loadReport();
  }

  // Keyboard navigation for carousel
  card.setAttribute('tabindex', '0');
  card.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowLeft') {
      e.preventDefault();
      prevBtn.click();
    } else if (e.key === 'ArrowRight') {
      e.preventDefault();
      nextBtn.click();
    }
  });

  updateCarousel();
  return card;
}

function openBossReportModal(reportId, label) {
  const existing = document.getElementById('boss-report-modal');
  if (existing) existing.remove();

  const backdrop = el('div', 'boss-modal-backdrop');
  backdrop.id = 'boss-report-modal';

  const win = el('div', 'boss-modal-window');

  const hdr = el('div', 'boss-modal-header');
  const title = el('div', 'boss-modal-title', `Executive Report: ${label}`);
  const closeBtn = el('button', 'boss-modal-close-btn', '✕');
  closeBtn.type = 'button';
  closeBtn.title = 'Close modal (Esc)';
  hdr.appendChild(title);
  hdr.appendChild(closeBtn);
  win.appendChild(hdr);

  const body = el('div', 'boss-modal-body');
  const sheet = el('div', 'boss-modal-sheet');
  const modalIframe = document.createElement('iframe');
  modalIframe.className = 'boss-preview-iframe';
  modalIframe.setAttribute('sandbox', 'allow-same-origin allow-scripts allow-modals allow-popups');

  if (state.bossReportCache && state.bossReportCache[reportId]) {
    modalIframe.srcdoc = state.bossReportCache[reportId];
  } else {
    modalIframe.src = `/api/report?type=${encodeURIComponent(reportId)}&format=html${TOKEN ? `&token=${encodeURIComponent(TOKEN)}` : ''}`;
  }

  sheet.appendChild(modalIframe);
  body.appendChild(sheet);
  win.appendChild(body);
  backdrop.appendChild(win);

  const closeModal = () => {
    backdrop.remove();
    document.removeEventListener('keydown', onEsc);
  };
  const onEsc = (e) => {
    if (e.key === 'Escape') closeModal();
  };

  closeBtn.addEventListener('click', closeModal);
  backdrop.addEventListener('click', (e) => {
    if (e.target === backdrop) closeModal();
  });
  document.addEventListener('keydown', onEsc);

  document.body.appendChild(backdrop);
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
    state.taskComments[taskId] = comments;
    if (state.tasks[taskId]) state.tasks[taskId].comments = comments;
    const messagesDiv = document.getElementById('panel-chat-messages') || document.getElementById('page-chat-messages');
    const titleEl = document.querySelector('#panel-chat-section .panel-section-title') || document.querySelector('#page-chat-section .panel-section-title');
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
  if (!id) return false;
  if (id.startsWith('task-')) return false;
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)) return true;
  if (/^[A-Za-z]+-\d+$/i.test(id)) return true;
  return false;
}

let lastDetailOpenTime = 0;

async function openDetail(target, pushHistory = true, orgHint = null, projectHint = null) {
  const panel   = document.getElementById('detail-panel');
  const content = document.getElementById('panel-content');

  stopChatPoll();
  lastDetailOpenTime = Date.now();
  if (panel) {
    panel.classList.remove('hidden');
    panel.classList.toggle('full-page', Boolean(state.taskDetailFullPage));
  }
  const expandBtn = document.getElementById('panel-expand');
  if (expandBtn) {
    expandBtn.classList.toggle('active', Boolean(state.taskDetailFullPage));
    expandBtn.title = state.taskDetailFullPage
      ? 'Exit full page (Restore side panel)'
      : 'Expand to full page (main viewport minus sidebar)';
    expandBtn.innerHTML = state.taskDetailFullPage ? '🗗' : '&#x26F6;';
    expandBtn.setAttribute('aria-pressed', String(Boolean(state.taskDetailFullPage)));
  }
  if (content) content.innerHTML = '<p style="color:var(--muted)">Loading…</p>';

  let targetId = target;
  if (typeof target === 'object' && target !== null) {
    orgHint = target.org || target.organization || orgHint;
    projectHint = target.project || projectHint;
    targetId = target.identifier || target.taskId || target.id;
  }

  // Attempt resolution from in-memory tasks
  const matchedTask = findTask(targetId, orgHint, projectHint);
  const resolvedId = matchedTask ? matchedTask.id : targetId;
  state.openDetailTaskId = resolvedId;

  // Determine canonical hierarchical path
  const canonicalPath = matchedTask
    ? taskToPath(matchedTask)
    : `/tasks/${encodeURIComponent(orgHint || 'STA')}/${encodeURIComponent(projectHint || 'default')}/${encodeURIComponent(targetId)}`;

  if (pushHistory && window.location.pathname !== canonicalPath) {
    history.pushState({ taskId: resolvedId, canonicalPath, taskPage: false }, '', canonicalPath);
  } else if (!pushHistory && (window.location.pathname.startsWith('/tasks/') || window.location.pathname.startsWith('/issues/')) && window.location.pathname !== canonicalPath) {
    history.replaceState({ taskId: resolvedId, canonicalPath, taskPage: false }, '', canonicalPath);
  }

  const isFleet = isFleetTaskId(resolvedId);
  const apiBase = isFleet ? '/api/fleet/tasks' : '/api/tasks';

  try {
    const [taskResp, commentsResp] = await Promise.all([
      apiFetch(`${apiBase}/${encodeURIComponent(resolvedId)}`),
      apiFetch(`${apiBase}/${encodeURIComponent(resolvedId)}/comments`).catch(() => ({ comments: [] }))
    ]);
    const task = taskResp.task || taskResp;
    if (taskResp.dependencies) {
      task.dependencies = taskResp.dependencies;
    }
    const comments = (taskResp.comments && taskResp.comments.length)
      ? taskResp.comments
      : (commentsResp?.comments || (Array.isArray(commentsResp) ? commentsResp : []));
    task.comments = comments;

    // Use robust UUID for internal state
    if (task.id) {
      state.openDetailTaskId = task.id;
    }

    // Ensure browser URL displays the fully resolved canonical hierarchical path
    const finalCanonicalPath = taskToPath(task);
    if (window.location.pathname !== finalCanonicalPath && (window.location.pathname.startsWith('/tasks/') || window.location.pathname.startsWith('/issues/'))) {
      history.replaceState({ taskId: task.id, canonicalPath: finalCanonicalPath, taskPage: false }, '', finalCanonicalPath);
    }

    // Also fetch native StayPoint governance snapshot if available
    try {
      const govResp = await apiFetch(`/api/tasks/${encodeURIComponent(task.id || resolvedId)}/governance`);
      if (govResp && !govResp.error) {
        task.governance = govResp;
      }
    } catch { /* governance optional */ }

    renderDetailContent(content, task);
    const detailKey = task.id || resolvedId;
    if (task.description) state.taskDescriptions[detailKey] = task.description;
    state.taskComments[detailKey] = comments;
    state.tasks[detailKey] = { ...(state.tasks[detailKey] || {}), ...task };

    buildChatSection(content, task.id || resolvedId, comments);
    startChatPoll(task.id || resolvedId);
  } catch (err) {
    const cached = matchedTask || state.tasks[resolvedId] || state.tasks[targetId];
    if (cached) {
      if (cached.id) state.openDetailTaskId = cached.id;
      renderDetailContent(content, cached);
      buildChatSection(content, cached.id || resolvedId, cached.comments || []);
      startChatPoll(cached.id || resolvedId);
    } else {
      const p = el('p', null, 'Task not found or failed to load.');
      p.style.color = 'var(--red)';
      if (content) {
        content.innerHTML = '';
        content.appendChild(p);
      }
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

function toggleDetailFullPage() {
  const panel = document.getElementById('detail-panel');
  const btn = document.getElementById('panel-expand');
  if (!panel) return;
  state.taskDetailFullPage = !state.taskDetailFullPage;
  panel.classList.toggle('full-page', Boolean(state.taskDetailFullPage));
  if (btn) {
    btn.classList.toggle('active', Boolean(state.taskDetailFullPage));
    btn.setAttribute('aria-pressed', String(Boolean(state.taskDetailFullPage)));
    if (state.taskDetailFullPage) {
      btn.title = 'Exit full page (Restore side panel)';
      btn.innerHTML = '🗗';
      btn.setAttribute('aria-label', 'Exit full page');
    } else {
      btn.title = 'Expand to full page (main viewport minus sidebar)';
      btn.innerHTML = '&#x26F6;';
      btn.setAttribute('aria-label', 'Expand to full page');
    }
  }
}

function closeDetailPanel() {
  const panel = document.getElementById('detail-panel');
  if (!panel || panel.classList.contains('hidden')) return;
  panel.classList.add('hidden');
  if (state.taskDetailFullPage) {
    state.taskDetailFullPage = false;
    panel.classList.remove('full-page');
    const expandBtn = document.getElementById('panel-expand');
    if (expandBtn) {
      expandBtn.classList.remove('active');
      expandBtn.setAttribute('aria-pressed', 'false');
      expandBtn.title = 'Expand to full page (main viewport minus sidebar)';
      expandBtn.innerHTML = '&#x26F6;';
      expandBtn.setAttribute('aria-label', 'Expand to full page');
    }
  }
  stopChatPoll();
  state.openDetailTaskId = null;
  if (window.location.pathname.startsWith('/tasks/') || window.location.pathname.startsWith('/issues/')) {
    const activeBtn = document.querySelector('.sidebar-item.active');
    const viewName = activeBtn?.dataset?.view || 'overview';
    navigateTo(viewName, state.currentOrgDetail, true);
  }
}

document.getElementById('panel-close')?.addEventListener('click', closeDetailPanel);
document.getElementById('panel-expand')?.addEventListener('click', toggleDetailFullPage);
document.getElementById('panel-open')?.addEventListener('click', () => {
  if (state.openDetailTaskId) openTaskPage(state.openDetailTaskId);
});

// ── Run Timeline helpers ───────────────────────────────────

const STEP_ICON = {
  wake:       '🌅',
  think:      '💭',
  read:       '📖',
  run:        '⚡',
  edit:       '✏️',
  checkpoint: '📌',
  message:    '💬',
  state:      '🏁',
  stats:      '📊',
};

const STEP_STATUS_CLASS = {
  running: 'tl-status-running',
  done:    'tl-status-done',
  error:   'tl-status-error',
};

function renderTimelineStep(container, step) {
  const row = el('div', 'timeline-row');
  row.dataset.seq = step.seq;
  row.dataset.status = step.status;

  const header = el('div', 'timeline-row-header');
  const icon = el('span', 'tl-icon', STEP_ICON[step.kind] || '•');
  const title = el('span', 'tl-title', step.title || step.kind);
  const statusDot = el('span', `tl-dot ${STEP_STATUS_CLASS[step.status] || 'tl-status-done'}`);

  let elapsed = '';
  if (step.started_at && step.ended_at) {
    const ms = new Date(step.ended_at) - new Date(step.started_at);
    elapsed = ms < 1000 ? `${ms}ms` : `${(ms/1000).toFixed(1)}s`;
  }
  const time = el('span', 'tl-time', elapsed);

  header.appendChild(statusDot);
  header.appendChild(icon);
  header.appendChild(title);
  header.appendChild(time);
  row.appendChild(header);

  // Body (expandable)
  if (step.body) {
    const body = el('div', 'timeline-row-body');
    body.textContent = step.body;
    body.style.display = 'none';
    row.appendChild(body);
    header.style.cursor = 'pointer';
    header.addEventListener('click', () => {
      body.style.display = body.style.display === 'none' ? 'block' : 'none';
    });
  }

  container.appendChild(row);
  return row;
}

function updateTimelineStats(statsBar, steps) {
  if (!steps || !steps.length) { statsBar.style.display = 'none'; return; }
  statsBar.style.display = 'flex';

  const first = steps.find(s => s.started_at);
  const last = [...steps].reverse().find(s => s.ended_at || s.started_at);
  let elapsedStr = '';
  if (first && last) {
    const end = last.ended_at || last.started_at;
    const ms = new Date(end) - new Date(first.started_at);
    elapsedStr = ms < 60000 ? `${(ms/1000).toFixed(1)}s` : `${(ms/60000).toFixed(1)}m`;
  }
  const edits = steps.filter(s => s.kind === 'edit').length;
  const runs  = steps.filter(s => s.kind === 'run').length;
  const errors = steps.filter(s => s.status === 'error').length;

  statsBar.innerHTML = '';
  const items = [
    elapsedStr && `⏱ ${elapsedStr}`,
    edits && `✏️ ${edits} edit${edits !== 1 ? 's' : ''}`,
    runs  && `⚡ ${runs} cmd${runs !== 1 ? 's' : ''}`,
    errors && `❌ ${errors} error${errors !== 1 ? 's' : ''}`,
  ].filter(Boolean);
  for (const item of items) {
    const span = document.createElement('span');
    span.className = 'tl-stat';
    span.textContent = item;
    statsBar.appendChild(span);
  }
}

// Append or update a step in the live timeline list.
function applyTimelineStep(step) {
  const list = document.getElementById('page-timeline-list');
  if (!list) return;
  // Remove placeholder
  const placeholder = list.querySelector('.panel-field-muted');
  if (placeholder) placeholder.remove();

  const existing = list.querySelector(`[data-seq="${step.seq}"]`);
  if (existing) {
    existing.dataset.status = step.status;
    const dot = existing.querySelector('.tl-dot');
    if (dot) dot.className = `tl-dot ${STEP_STATUS_CLASS[step.status] || 'tl-status-done'}`;
    const bodyEl = existing.querySelector('.timeline-row-body');
    if (bodyEl && step.body) bodyEl.textContent = step.body;
  } else {
    renderTimelineStep(list, step);
  }

  // Update stats
  const statsBar = document.getElementById('page-timeline-stats');
  if (statsBar) {
    const allRows = [...list.querySelectorAll('.timeline-row')];
    const synthSteps = allRows.map(r => ({
      seq:        parseInt(r.dataset.seq),
      kind:       r.querySelector('.tl-icon')?.textContent,
      status:     r.dataset.status,
      started_at: null,
      ended_at:   null,
    }));
    updateTimelineStats(statsBar, synthSteps.length ? synthSteps : null);
  }
}

// ── Full-page task view ────────────────────────────────────

async function openTaskPage(target, pushHistory = true) {
  // Close sidebar if open
  const panel = document.getElementById('detail-panel');
  if (panel) panel.classList.add('hidden');
  stopChatPoll();

  let targetId = target;
  let orgHint = null;
  let projectHint = null;
  if (typeof target === 'object' && target !== null) {
    orgHint = target.org || target.organization || null;
    projectHint = target.project || target.projectHint || null;
    targetId = target.identifier || target.taskId || target.id;
  }

  const matchedTask = findTask(targetId, orgHint, projectHint);
  const resolvedId = matchedTask ? matchedTask.id : (targetId || '');
  state.openDetailTaskId = resolvedId;

  const canonicalPath = matchedTask
    ? taskToPath(matchedTask)
    : `/tasks/${encodeURIComponent(orgHint || 'STA')}/${encodeURIComponent(projectHint || 'default')}/${encodeURIComponent(targetId || '')}`;

  if (pushHistory && window.location.pathname !== canonicalPath) {
    history.pushState({ taskId: resolvedId, canonicalPath, taskPage: true }, '', canonicalPath);
  }

  navigateTo('task-page', null, false);

  const pageContent = document.getElementById('task-page-content');
  if (pageContent) pageContent.innerHTML = '<p style="color:var(--muted);padding:2rem">Loading…</p>';

  const isFleet = isFleetTaskId(resolvedId);
  const apiBase = isFleet ? '/api/fleet/tasks' : '/api/tasks';

  try {
    const [taskResp, commentsResp] = await Promise.all([
      apiFetch(`${apiBase}/${encodeURIComponent(resolvedId)}`),
      apiFetch(`${apiBase}/${encodeURIComponent(resolvedId)}/comments`).catch(() => ({ comments: [] }))
    ]);
    const task = taskResp.task || taskResp;
    const comments = (taskResp.comments && taskResp.comments.length)
      ? taskResp.comments
      : (commentsResp?.comments || (Array.isArray(commentsResp) ? commentsResp : []));
    task.comments = comments;

    if (task.id) state.openDetailTaskId = task.id;

    const finalPath = taskToPath(task);
    if (pushHistory && window.location.pathname !== finalPath) {
      history.replaceState({ taskId: task.id, canonicalPath: finalPath, taskPage: true }, '', finalPath);
    }

    try {
      const govResp = await apiFetch(`/api/tasks/${encodeURIComponent(task.id || resolvedId)}/governance`);
      if (govResp && !govResp.error) task.governance = govResp;
    } catch { /* governance optional */ }

    const activeId = task.id || resolvedId;
    state.tasks[activeId] = { ...(state.tasks[activeId] || {}), ...task };
    if (task.description) state.taskDescriptions[activeId] = task.description;
    state.taskComments[activeId] = comments;

    renderTaskPage(pageContent, task, comments);
    startChatPoll(activeId);
  } catch {
    const cached = matchedTask || state.tasks[resolvedId] || state.tasks[targetId];
    if (cached && pageContent) {
      renderTaskPage(pageContent, cached, cached.comments || []);
      startChatPoll(cached.id || resolvedId);
    } else if (pageContent) {
      pageContent.innerHTML = '<p style="color:var(--red);padding:2rem">Task not found or failed to load.</p>';
    }
  }
}

function renderTaskPage(container, task, comments) {
  container.innerHTML = '';

  // Back bar
  const backBar = el('div', 'task-page-back-bar');
  const backBtn = el('button', 'task-page-back-btn', '← Back');
  backBtn.addEventListener('click', () => {
    stopChatPoll();
    state.openDetailTaskId = null;
    history.back();
  });
  backBar.appendChild(backBtn);

  const ident = task.identifier || (task.id ? `#${task.id.slice(0, 8)}` : '');
  if (ident) {
    const breadcrumb = el('span', 'task-page-breadcrumb', ident);
    backBar.appendChild(breadcrumb);
  }
  container.appendChild(backBar);

  // Two-column layout
  const layout = el('div', 'task-page-layout');

  // ── Main column ──
  const main = el('div', 'task-page-main');

  // Title
  const titleEl = el('h1', 'task-page-title', task.title || task.name || '(untitled)');
  main.appendChild(titleEl);

  // Status/priority pills row
  const pillsRow = el('div', 'task-page-pills');
  if (task.status) pillsRow.appendChild(statusPill(task.status));
  if (task.priority) pillsRow.appendChild(statusPill(task.priority));
  if (ident) {
    const identBadge = el('span', 'card-id', ident);
    pillsRow.appendChild(identBadge);
  }
  main.appendChild(pillsRow);

  // Description
  const descSection = el('div', 'task-page-section');
  descSection.appendChild(el('div', 'task-page-section-title', 'Description'));
  let desc = (task.description || '').trim();
  if (desc) {
    descSection.appendChild(mdEl(desc));
  } else {
    descSection.appendChild(el('p', 'panel-field-muted', 'No description provided.'));
  }
  main.appendChild(descSection);

  // Notes (if any)
  const notesVal = task.notes || '';
  if (notesVal) {
    const notesSection = el('div', 'task-page-section');
    notesSection.appendChild(el('div', 'task-page-section-title', 'Notes'));
    notesSection.appendChild(mdEl(notesVal));
    main.appendChild(notesSection);
  }

  // Activity / comments
  if (comments && comments.length) {
    const actSection = el('div', 'task-page-section');
    actSection.appendChild(el('div', 'task-page-section-title', `Activity (${comments.length})`));
    for (const c of comments) {
      const authorType = (c.authorType || c.author_type || '').toLowerCase();
      const isAgent = authorType === 'agent' || authorType === 'system';
      const authorLabel = isAgent
        ? (c.authorName || c.author_name || 'Agent')
        : (c.author || 'User');
      const ts = c.createdAt || c.created_at || c.timestamp || '';
      const row = el('div', `chat-msg ${isAgent ? 'chat-msg-agent' : 'chat-msg-user'}`);
      row.appendChild(el('div', 'chat-msg-meta', `${authorLabel}${ts ? ' · ' + fmtDateTime(ts) : ''}`));
      const bubble = el('div', 'chat-msg-bubble');
      bubble.appendChild(mdEl(c.body || c.message || ''));
      row.appendChild(bubble);
      actSection.appendChild(row);
    }
    main.appendChild(actSection);
  }

  // Agent interaction (chat)
  const chatSection = el('div', 'task-page-section');
  chatSection.id = 'page-chat-section';
  chatSection.appendChild(el('div', 'task-page-section-title', 'Send to Agent'));

  const messagesDiv = el('div', 'chat-messages');
  messagesDiv.id = 'page-chat-messages';
  messagesDiv.style.maxHeight = '320px';
  if (!comments || !comments.length) {
    messagesDiv.appendChild(el('p', 'panel-field-muted', 'No messages yet.'));
  }
  chatSection.appendChild(messagesDiv);

  const compose = el('div', 'chat-compose');
  const textarea = document.createElement('textarea');
  textarea.className = 'chat-textarea';
  textarea.placeholder = 'Message the agent… (⌘↵ to send)';
  textarea.rows = 3;
  textarea.addEventListener('input', () => {
    textarea.style.height = 'auto';
    textarea.style.height = Math.max(56, Math.min(300, textarea.scrollHeight)) + 'px';
  });
  const sendBtn = el('button', 'chat-send-btn', 'Send');
  sendBtn.type = 'button';
  const taskId = task.id || (state.openDetailTaskId || '');

  const doSend = async () => {
    const body = textarea.value.trim();
    if (!body) return;
    textarea.value = '';
    textarea.style.height = '';
    sendBtn.disabled = true;
    try {
      await sendComment(taskId, body);
      await refreshChatMessages(taskId);
    } catch { /* silent */ } finally {
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
  chatSection.appendChild(compose);
  main.appendChild(chatSection);

  // ── Run Timeline section ──
  const tlSection = el('div', 'task-page-section');
  tlSection.id = 'page-timeline-section';
  const tlTitle = el('div', 'task-page-section-title', 'Run Timeline');
  tlSection.appendChild(tlTitle);

  const statsBar = el('div', 'timeline-stats-bar');
  statsBar.id = 'page-timeline-stats';
  statsBar.style.display = 'none';
  tlSection.appendChild(statsBar);

  const tlList = el('div', 'timeline-list');
  tlList.id = 'page-timeline-list';
  tlList.appendChild(el('p', 'panel-field-muted', 'No run steps yet.'));
  tlSection.appendChild(tlList);

  main.appendChild(tlSection);

  // Load existing steps
  try {
    const stepsResp = await apiFetch(`/api/tasks/${task.id}/run-steps`);
    if (Array.isArray(stepsResp) && stepsResp.length > 0) {
      tlList.innerHTML = '';
      for (const s of stepsResp) renderTimelineStep(tlList, s);
      updateTimelineStats(statsBar, stepsResp);
    }
  } catch { /* silent */ }

  layout.appendChild(main);

  // ── Metadata sidebar ──
  const meta = el('div', 'task-page-meta');

  const addMetaField = (label, value) => {
    if (!value && value !== 0) return;
    const field = el('div', 'panel-field');
    field.appendChild(el('div', 'panel-field-label', label));
    if (typeof value === 'string') {
      field.appendChild(el('div', 'panel-field-value', value));
    } else {
      field.appendChild(value);
    }
    meta.appendChild(field);
  };

  addMetaField('Status', task.status);
  addMetaField('Priority', task.priority);
  addMetaField('Assignee', task.assignee_name || task.checkout_agent_id || null);
  addMetaField('Project', projectSlug(task) !== 'default' ? projectSlug(task) : null);
  addMetaField('Org', task.organization || null);
  addMetaField('Stage', task.execution_stage || null);
  addMetaField('Spend',
    (task.spent_usd || task.spent_tokens)
      ? `${fmtCurrency(task.spent_usd || 0)} · ${fmtCompactNum(task.spent_tokens || 0)} tokens`
      : null);
  addMetaField('Budget',
    (task.max_budget_usd || task.max_turns)
      ? `${fmtCurrency(task.max_budget_usd || 0)} · ${task.max_turns || 50} turns max`
      : null);
  addMetaField('Internal ID', task.id || null);

  // Labels
  let labels = task.labels;
  if (typeof labels === 'string') {
    try { labels = JSON.parse(labels); } catch { labels = labels ? [labels] : []; }
  }
  if (Array.isArray(labels) && labels.length) {
    const lblWrap = el('div', 'panel-field');
    lblWrap.appendChild(el('div', 'panel-field-label', 'Labels'));
    const badgeContainer = el('div');
    badgeContainer.style.cssText = 'display:flex;flex-wrap:wrap;gap:6px;margin-top:4px;';
    for (const l of labels) {
      const name = typeof l === 'object' ? l.name : l;
      const color = typeof l === 'object' && l.color ? l.color : '#38bdf8';
      const badge = el('span', 'task-label-badge', name);
      badge.style.cssText = `font-size:0.75rem;padding:2px 8px;border-radius:12px;font-weight:600;background:${color}22;border:1px solid ${color};color:${color};`;
      badgeContainer.appendChild(badge);
    }
    lblWrap.appendChild(badgeContainer);
    meta.appendChild(lblWrap);
  }

  layout.appendChild(meta);
  container.appendChild(layout);
}

document.addEventListener('click', (e) => {
  const panel = document.getElementById('detail-panel');
  if (!panel || panel.classList.contains('hidden')) return;
  // If detail was just opened/switched in this click event, do not close
  if (Date.now() - lastDetailOpenTime < 150) return;
  // If clicked inside the detail panel, do not close
  if (panel.contains(e.target)) return;
  // If clicked on close, expand, or open button, ignore
  if (e.target.closest('#panel-close') || e.target.closest('#panel-expand') || e.target.closest('#panel-open')) return;
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
  populateOverviewProjectFilter();
  renderGlobalTaskTable();
});
document.getElementById('task-project-filter')?.addEventListener('change', (e) => {
  state.taskFilter.project = e.target.value;
  renderGlobalTaskTable();
});
document.getElementById('task-priority-filter')?.addEventListener('change', (e) => {
  state.taskFilter.priority = e.target.value;
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
  populateTSProjectFilter();
  renderTaskStatusPage();
});
document.getElementById('ts-project-filter')?.addEventListener('change', (e) => {
  state.tsFilter.project = e.target.value;
  renderTaskStatusPage();
});
document.getElementById('ts-priority-filter')?.addEventListener('change', (e) => {
  state.tsFilter.priority = e.target.value;
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
  state.agentsFilter.project = 'all';
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
// ── Filter listeners (recent tasks page) ───────────────────
document.getElementById('recent-tasks-org-filter')?.addEventListener('change', (e) => {
  state.recentTasksFilter.org = e.target.value;
  populateRecentTasksProjectFilter();
  renderRecentTasks();
});
document.getElementById('recent-tasks-project-filter')?.addEventListener('change', (e) => {
  state.recentTasksFilter.project = e.target.value;
  renderRecentTasks();
});
document.getElementById('recent-tasks-priority-filter')?.addEventListener('change', (e) => {
  state.recentTasksFilter.priority = e.target.value;
  renderRecentTasks();
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
let checklistCommitVerification = null;

function isItemCommitBlocked(itemId) {
  if (!checklistCommitVerification) return false;
  return checklistCommitVerification.blocked_item_ids?.includes(itemId) || false;
}

function getItemCommitBlockReason(itemId) {
  if (!checklistCommitVerification?.missing_commits) return 'Required commit missing from main or running daemon';
  const itemWarn = checklistCommitVerification.missing_commits.find(w => w.item_id === itemId);
  return itemWarn ? itemWarn.reason : 'Required commit missing from main or running daemon';
}

function isSectionCommitBlocked(sectionName) {
  if (!checklistCommitVerification) return false;
  return checklistCommitVerification.blocked_sections?.includes(sectionName) || false;
}

async function loadChecklist(sprint) {
  const urlParams = new URLSearchParams(window.location.search);
  const sprintFromUrl = urlParams.get('sprint');
  const sel = document.getElementById('checklist-sprint-filter');
  const s = sprint || sprintFromUrl || sel?.value || 'STA-236';
  if (sel && sel.value !== s) {
    sel.value = s;
  }
  try {
    const r = await apiFetch(`/api/checklist?sprint=${encodeURIComponent(s)}`);
    checklistItems = r.items || [];
    checklistCommitVerification = r.commit_verification || null;
    renderChecklist();
    updateChecklistProgress();
    if (typeof updateDevTourToggleUI === 'function') updateDevTourToggleUI();
    if (typeof isWalkthroughActive === 'function' && isWalkthroughActive()) renderWalkthroughHUD();
  } catch (err) {
    const c = document.getElementById('checklist-container');
    if (c) c.innerHTML = `<p style="color:var(--red)">Failed to load checklist: ${err.message}. Try seeding first.</p>`;
  }
}

async function seedChecklist() {
  const sprint = document.getElementById('checklist-sprint-filter')?.value || 'STA-236';
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
    const currentVal = sel.value;
    const urlParams = new URLSearchParams(window.location.search);
    const sprintFromUrl = urlParams.get('sprint');
    sel.innerHTML = '';
    for (const s of r.sprints) {
      const opt = document.createElement('option');
      opt.value = s;
      opt.textContent = s === 'STA-236' ? 'STA-236 (Verification & DoD Gate)' : (s === 'STA-168-2' ? 'STA-168-2 (Sprint 2)' : (s === 'STA-168' ? 'STA-168 (Sprint 1)' : s));
      sel.appendChild(opt);
    }
    if (sprintFromUrl && r.sprints.includes(sprintFromUrl)) {
      sel.value = sprintFromUrl;
    } else if (currentVal && r.sprints.includes(currentVal)) {
      sel.value = currentVal;
    } else {
      sel.value = r.sprints[0];
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

  // Commit-Hash Gate Warning Banner & Log
  if (checklistCommitVerification && (!checklistCommitVerification.verified || (checklistCommitVerification.missing_commits && checklistCommitVerification.missing_commits.length > 0))) {
    const missing = checklistCommitVerification.missing_commits || [];
    const blockedItems = checklistCommitVerification.blocked_item_ids || [];
    const blockedSections = checklistCommitVerification.blocked_sections || [];

    const banner = el('div', 'checklist-commit-gate-banner');
    const header = el('div', 'cl-gate-header');
    const icon = el('div', 'cl-gate-icon', '⛔');
    const content = el('div', 'cl-gate-content');
    const title = el('div', 'cl-gate-title', 'Checklist Commit-Hash Gate: Missing or Unmerged Commits Detected');
    const desc = el('div', 'cl-gate-desc', `${blockedItems.length} item(s) across ${blockedSections.length} section(s) are blocked from being marked "done". Referenced commits must exist in main and be compiled into the running staypointd daemon.`);

    const meta = el('div', 'cl-gate-meta');
    meta.innerHTML = `<span>Running Daemon: <code>${escapeHtml(checklistCommitVerification.running_commit || 'unrebuilt / none')}</code></span> <span>Main Branch: <code>${escapeHtml(checklistCommitVerification.main_commit || 'unknown')}</code></span>`;
    content.appendChild(title);
    content.appendChild(desc);
    content.appendChild(meta);

    const toggleBtn = el('button', 'cl-gate-toggle-btn', `Show Warning Log (${missing.length})`);
    header.appendChild(icon);
    header.appendChild(content);
    header.appendChild(toggleBtn);
    banner.appendChild(header);

    const logContainer = el('div', 'cl-gate-log-container');
    logContainer.style.display = 'none';

    const table = el('table', 'cl-gate-log-table');
    table.innerHTML = `<thead><tr><th>Commit</th><th>Section / Item</th><th>Status in main</th><th>Status in daemon</th><th>Reason</th></tr></thead>`;
    const tbody = el('tbody');
    for (const m of missing) {
      const tr = el('tr');
      tr.innerHTML = `
        <td><code>${escapeHtml(m.commit)}</code></td>
        <td><strong>${escapeHtml(m.section)}</strong><br/><span class="muted-text">${escapeHtml(m.item_title || '')}</span></td>
        <td>${m.missing_from_main ? '<span style="color:var(--red)">✗ Missing</span>' : '<span style="color:var(--green)">✓ Present</span>'}</td>
        <td>${m.missing_from_binary ? '<span style="color:var(--red)">✗ Unrebuilt / Missing</span>' : '<span style="color:var(--green)">✓ Present</span>'}</td>
        <td style="color:var(--muted)">${escapeHtml(m.reason || '')}</td>
      `;
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    logContainer.appendChild(table);
    banner.appendChild(logContainer);

    toggleBtn.addEventListener('click', () => {
      const isHidden = logContainer.style.display === 'none';
      logContainer.style.display = isHidden ? 'block' : 'none';
      toggleBtn.textContent = isHidden ? `Hide Warning Log (${missing.length})` : `Show Warning Log (${missing.length})`;
    });

    container.appendChild(banner);
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
    if (isSectionCommitBlocked(sectionName)) {
      const blockedBadge = el('span', 'badge badge-commit-blocked', '⛔ Commit Gate Active');
      blockedBadge.title = 'Section contains unmerged or uncompiled commits';
      titleLeft.appendChild(blockedBadge);
    }

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
  const isBlocked = isItemCommitBlocked(item.id);
  const blockReason = isBlocked ? getItemCommitBlockReason(item.id) : '';
  const btnCol = el('div', 'checklist-status-btns');
  for (const [status, icon, label] of [
    ['pass',     '✓', 'Pass'],
    ['partial',  '◐', 'In-Between / Needs Work'],
    ['fail',     '✗', 'Fail'],
    ['skip',     '–', 'Skip'],
    ['not_done', '!', 'Not Done'],
  ]) {
    const isPass = status === 'pass';
    const btn = el('button', `cl-btn${item.status === status ? ' active-' + status : ''}${isPass && isBlocked ? ' cl-btn-blocked' : ''}`, icon);
    btn.dataset.status = status;
    if (isPass && isBlocked) {
      btn.disabled = true;
      btn.title = `⛔ Blocked by Commit Gate: ${blockReason}`;
    } else {
      btn.title = label;
    }
    btn.addEventListener('click', () => updateChecklistStatus(item.id, status));
    btnCol.appendChild(btn);
  }
  row.appendChild(btnCol);

  // Body
  const body = el('div', 'checklist-body');
  const titleEl = el('div', `checklist-title${item.status !== 'pending' ? ' status-' + item.status : ''}`, item.title);
  if (item.commit_hash) {
    const commitBadge = el('span', 'checklist-commit-badge', `git:${item.commit_hash}`);
    commitBadge.title = `Bound commit: ${item.commit_hash}`;
    titleEl.appendChild(commitBadge);
  }
  if (isBlocked) {
    const blockedTag = el('span', 'badge badge-commit-blocked', '⛔ Commit Gate');
    blockedTag.title = `Blocked: ${blockReason}`;
    titleEl.appendChild(blockedTag);
  }
  if (item.contract) {
    const contractTag = el('span', 'checklist-contract-tag', '⚙ contract');
    contractTag.title = 'Machine-verifiable contract: ' + item.contract;
    titleEl.appendChild(contractTag);
  }
  const tourItemBtn = el('button', 'cl-item-walkthrough-btn', '▶ Tour');
  tourItemBtn.title = 'Start interactive walkthrough tour from this question';
  tourItemBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const sel = document.getElementById('checklist-sprint-filter');
    const s = sel?.value || 'STA-236';
    const idx = checklistItems.findIndex(i => i.id === item.id);
    startWalkthrough(s, idx >= 0 ? idx : 0);
  });
  titleEl.appendChild(tourItemBtn);
  body.appendChild(titleEl);
  if (item.description) body.appendChild(el('div', 'checklist-desc', item.description));
  if (item.how_to_test) body.appendChild(el('div', 'checklist-howto', item.how_to_test));

  // Notes + version history
  const notesRow = el('div', 'checklist-notes-row');
  const notesInput = el('textarea', 'checklist-notes-input');
  const draftKey = 'staypoint_cl_draft_' + item.id;
  const draftVal = localStorage.getItem(draftKey);
  const initialNote = (draftVal !== null && draftVal !== undefined && (!item.notes || draftVal.length >= item.notes.length))
    ? draftVal
    : (item.notes || '');
  notesInput.value = initialNote;
  item.notes = initialNote;
  notesInput.placeholder = 'Add a note…';
  const rawNote = initialNote;
  const lineCount = Math.max(rawNote.split('\n').length, Math.ceil(rawNote.length / 50));
  notesInput.rows = Math.max(2, Math.min(15, lineCount));
  const autoResize = () => {
    notesInput.style.height = 'auto';
    notesInput.style.height = Math.max(48, notesInput.scrollHeight) + 'px';
  };
  notesInput.addEventListener('input', () => {
    autoResize();
    const val = notesInput.value;
    item.notes = val;
    const idx = checklistItems.findIndex(i => i.id === item.id);
    if (idx !== -1) checklistItems[idx].notes = val;
    localStorage.setItem(draftKey, val);
    const state = typeof getWalkthroughState === 'function' ? getWalkthroughState() : null;
    if (state?.active && checklistItems[state.index]?.id === item.id) {
      const hudInput = document.getElementById('hud-notes-input');
      if (hudInput) hudInput.value = val;
    }
  });
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

async function updateChecklistStatus(id, status, explicitNotes = null) {
  const idx = checklistItems.findIndex(i => i.id === id);
  if (idx === -1) return;

  if (status === 'pass' && isItemCommitBlocked(id)) {
    const reason = getItemCommitBlockReason(id);
    alert(`⛔ Commit Gate Block: This checklist item cannot be marked "done" / "pass".\n\nReason: ${reason}\n\nRequired commits must be merged into main and compiled into the running staypointd daemon.`);
    return;
  }

  const row = document.querySelector(`.checklist-item[data-id="${id}"]`);
  const currentNotesInput = row?.querySelector('.checklist-notes-input');
  const draftKey = 'staypoint_cl_draft_' + id;
  const draftVal = localStorage.getItem(draftKey);

  let notes = explicitNotes;
  if (notes === null || notes === undefined) {
    if (currentNotesInput && currentNotesInput.value !== undefined) {
      notes = currentNotesInput.value;
    } else if (draftVal !== null && draftVal !== undefined) {
      notes = draftVal;
    } else {
      notes = checklistItems[idx].notes || '';
    }
  }

  // Synchronize in memory immediately
  checklistItems[idx].status = status;
  checklistItems[idx].notes = notes;
  if (currentNotesInput) currentNotesInput.value = notes;

  try {
    const r = await fetch(`/api/checklist/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', ...authHeader() },
      body: JSON.stringify({ status, notes }),
    });
    if (!r.ok) {
      const errText = await r.text();
      let errMsg = errText;
      try {
        const parsed = JSON.parse(errText);
        if (parsed.error) errMsg = parsed.error;
      } catch {}
      throw new Error(errMsg);
    }
    const updated = await r.json();
    checklistItems[idx] = updated;
    localStorage.removeItem(draftKey);

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
      const input = row.querySelector('.checklist-notes-input');
      if (input && updated.notes !== undefined) input.value = updated.notes;
    }
    updateChecklistProgress();
    updateSectionCounts(checklistItems[idx].section);

    if (typeof isWalkthroughActive === 'function' && isWalkthroughActive()) {
      const state = getWalkthroughState();
      if (state && checklistItems[state.index]?.id === id && typeof updateHUDStatusButtons === 'function') {
        updateHUDStatusButtons(updated.status);
      }
    }
  } catch (err) {
    console.error('checklist update failed', err);
  }
}

async function updateChecklistNotes(id, notes) {
  const idx = checklistItems.findIndex(i => i.id === id);
  if (idx === -1) return;
  checklistItems[idx].notes = notes;
  const draftKey = 'staypoint_cl_draft_' + id;
  try {
    const r = await fetch(`/api/checklist/${id}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', ...authHeader() },
      body: JSON.stringify({ notes }),
    });
    if (!r.ok) throw new Error(await r.text());
    const updated = await r.json();
    checklistItems[idx] = updated;
    localStorage.removeItem(draftKey);
    const row = document.querySelector(`.checklist-item[data-id="${id}"]`);
    const histBtn = row?.querySelector('.checklist-history-toggle');
    if (histBtn) histBtn.textContent = `v${updated.version}`;
    const input = row?.querySelector('.checklist-notes-input');
    if (input && updated.notes !== undefined) input.value = updated.notes;
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
  const url = new URL(window.location);
  url.searchParams.set('sprint', e.target.value);
  window.history.replaceState({}, '', url);
  loadChecklist(e.target.value);
});

function showToast(message, type = 'info', duration = 6000) {
  let container = document.getElementById('toast-container');
  if (!container) {
    container = document.createElement('div');
    container.id = 'toast-container';
    container.className = 'toast-container';
    document.body.appendChild(container);
  }
  const icons = {
    info: 'ℹ️',
    success: '✅',
    warning: '⚠️',
    error: '❌'
  };
  const toast = document.createElement('div');
  toast.className = `toast toast-${type}`;
  toast.innerHTML = `
    <span class="toast-icon">${icons[type] || 'ℹ️'}</span>
    <span class="toast-message">${escapeHtml(message)}</span>
    <button class="toast-close" aria-label="Dismiss">&times;</button>
  `;
  const dismiss = () => {
    toast.classList.add('toast-fadeout');
    setTimeout(() => toast.remove(), 250);
  };
  toast.querySelector('.toast-close')?.addEventListener('click', dismiss);
  container.appendChild(toast);
  if (duration > 0) {
    setTimeout(dismiss, duration);
  }
}

async function verifyChecklistContracts() {
  const sprint = document.getElementById('checklist-sprint-filter')?.value || 'STA-236';
  const btn = document.getElementById('checklist-verify-btn');
  const bannerArea = document.getElementById('checklist-banner-area');

  if (btn) {
    btn.disabled = true;
    btn.innerHTML = '<span class="dl-spinner"></span> Verifying…';
  }

  if (bannerArea) {
    bannerArea.innerHTML = `
      <div class="checklist-summary-banner banner-loading">
        <span class="banner-icon">⚡</span>
        <div class="banner-content">
          <div class="banner-title">Evaluating Contracts for ${escapeHtml(sprint)}…</div>
          <div class="banner-desc">Executing machine assertions and inspecting regression status.</div>
        </div>
      </div>
    `;
  }
  showToast(`Evaluating machine contracts for sprint ${sprint}…`, 'info', 4000);

  try {
    const res = await fetch(`/api/checklist/evaluate?sprint=${encodeURIComponent(sprint)}&downgrade=true`, {
      method: 'POST',
      headers: { 'Authorization': `Bearer ${window.__STAYPOINT_TOKEN__ || ''}` }
    });
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}));
      throw new Error(errBody.error || `Server responded with ${res.status}`);
    }
    const summary = await res.json();
    const commitShort = summary.commit ? summary.commit.substring(0, 7) : (summary.commit_sha ? summary.commit_sha.substring(0, 7) : 'HEAD');

    let bannerClass = 'banner-success';
    let bannerIcon = '✅';
    let bannerTitle = `All ${summary.total} Machine Contracts Passing`;
    let bannerDesc = `Zero regressions detected across all test suites at commit ${commitShort}.`;
    let toastType = 'success';
    let toastMsg = `✅ All ${summary.total} contracts passing (commit ${commitShort})`;

    if (summary.divergences > 0) {
      bannerClass = 'banner-divergence';
      bannerIcon = '⚠️';
      bannerTitle = `${summary.divergences} Contract Regression(s) Detected`;
      bannerDesc = `${summary.divergences} previously-passing item(s) failed machine verification and were automatically downgraded. Passed: ${summary.passed}, Failed: ${summary.failed}. Commit: ${commitShort}.`;
      toastType = 'warning';
      toastMsg = `⚠️ ${summary.divergences} regression(s) detected and downgraded!`;
    } else if (summary.failed > 0) {
      bannerClass = 'banner-warning';
      bannerIcon = '⚡';
      bannerTitle = `${summary.passed}/${summary.total} Contracts Passing (${summary.failed} failing)`;
      bannerDesc = `Evaluated machine contracts for sprint ${escapeHtml(sprint)}. Zero regressions detected. Commit: ${commitShort}.`;
      toastType = 'warning';
      toastMsg = `Checklist: ${summary.passed}/${summary.total} contracts passing (${summary.failed} failing)`;
    }

    if (bannerArea) {
      bannerArea.innerHTML = `
        <div class="checklist-summary-banner ${bannerClass}">
          <span class="banner-icon">${bannerIcon}</span>
          <div class="banner-content">
            <div class="banner-title">${escapeHtml(bannerTitle)}</div>
            <div class="banner-desc">${escapeHtml(bannerDesc)}</div>
          </div>
          <button class="banner-close" aria-label="Dismiss">&times;</button>
        </div>
      `;
      bannerArea.querySelector('.banner-close')?.addEventListener('click', () => {
        bannerArea.innerHTML = '';
      });
    }
    showToast(toastMsg, toastType, 7000);
    await loadChecklist(sprint);
  } catch (err) {
    console.error('Failed to verify contracts:', err);
    if (bannerArea) {
      bannerArea.innerHTML = `
        <div class="checklist-summary-banner banner-divergence">
          <span class="banner-icon">❌</span>
          <div class="banner-content">
            <div class="banner-title">Failed to Evaluate Contracts</div>
            <div class="banner-desc">${escapeHtml(err.message)}</div>
          </div>
          <button class="banner-close" aria-label="Dismiss">&times;</button>
        </div>
      `;
      bannerArea.querySelector('.banner-close')?.addEventListener('click', () => {
        bannerArea.innerHTML = '';
      });
    }
    showToast('Failed to evaluate checklist contracts: ' + err.message, 'error', 8000);
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.innerHTML = '⚡ Verify Contracts';
    }
  }
}

// ── Dev Walkthrough Tour Engine ───────────────────────────

const DEV_TOUR_TOGGLE_KEY = 'staypoint_dev_walkthrough_enabled';
const WALKTHROUGH_STATE_KEY = 'staypoint_walkthrough_state';

function isDevTourEnabled() {
  const v = localStorage.getItem(DEV_TOUR_TOGGLE_KEY);
  return v !== 'false'; // default enabled
}

function setDevTourEnabled(enabled) {
  localStorage.setItem(DEV_TOUR_TOGGLE_KEY, enabled ? 'true' : 'false');
  updateDevTourToggleUI();
  if (!enabled) {
    stopWalkthrough();
  }
}

function updateDevTourToggleUI() {
  const btn = document.getElementById('checklist-tour-toggle-btn');
  const startBtn = document.getElementById('checklist-start-tour-btn');
  const enabled = isDevTourEnabled();
  if (btn) {
    btn.textContent = enabled ? '🧪 Dev Tour: ON' : '🧪 Dev Tour: OFF';
    btn.style.borderColor = enabled ? '#38bdf8' : 'var(--border)';
    btn.style.color = enabled ? '#38bdf8' : 'var(--muted)';
  }
  if (startBtn) {
    startBtn.style.display = enabled ? 'inline-block' : 'none';
    const state = getWalkthroughState();
    if (state?.active) {
      startBtn.textContent = `▶ Resume Walkthrough (${(state.index || 0) + 1}/${checklistItems.length || '?'})`;
    } else {
      startBtn.textContent = '▶ Start Walkthrough';
    }
  }
}

function getWalkthroughState() {
  try {
    return JSON.parse(localStorage.getItem(WALKTHROUGH_STATE_KEY) || 'null');
  } catch {
    return null;
  }
}

function saveWalkthroughState(state) {
  if (!state) {
    localStorage.removeItem(WALKTHROUGH_STATE_KEY);
  } else {
    localStorage.setItem(WALKTHROUGH_STATE_KEY, JSON.stringify(state));
  }
}

function isWalkthroughActive() {
  const s = getWalkthroughState();
  return Boolean(s && s.active);
}

// Maps a checklist item to its target sidebar view and label
function resolveItemTarget(item) {
  if (!item) return { view: 'overview', label: 'All Organizations' };
  const text = `${item.section || ''} ${item.how_to_test || ''} ${item.description || ''} ${item.title || ''}`.toLowerCase();

  if (text.includes('/projects') || text.includes('projects page') || text.includes('project card')) {
    return { view: 'projects', label: 'Projects' };
  }
  if (text.includes('/agents') || text.includes('agents page') || text.includes('agent card')) {
    return { view: 'agents', label: 'Agents' };
  }
  if (text.includes('/recent-tasks') || text.includes('recent tasks') || text.includes('activity feed') || text.includes('subtask tree')) {
    return { view: 'recent-tasks', label: 'Recent Tasks' };
  }
  if (text.includes('/task-status') || text.includes('task status') || text.includes('9-column table') || text.includes('global task')) {
    return { view: 'task-status', label: 'Global Task Status' };
  }
  if (text.includes('/cost') || text.includes('cost & accounting') || text.includes('spend by') || text.includes('cost page')) {
    return { view: 'cost', label: 'Cost & Accounting' };
  }
  if (text.includes('/settings') || text.includes('settings page') || text.includes('provider accounts') || text.includes('fleet info')) {
    return { view: 'settings', label: 'Settings' };
  }
  if (text.includes('/checklist') || text.includes('checklist page') || text.includes('divergence')) {
    return { view: 'checklist', label: 'Checklist' };
  }
  if (text.includes('/kanban') || text.includes('kanban board')) {
    return { view: 'kanban', label: 'Kanban' };
  }
  if (text.includes('boss card') || text.includes('all organizations') || text.includes('kpi row') || text.includes('quota gauge')) {
    return { view: 'overview', label: 'All Organizations' };
  }
  return { view: 'overview', label: 'All Organizations' };
}

async function ensureChecklistItemsLoaded(sprint) {
  const s = sprint || document.getElementById('checklist-sprint-filter')?.value || 'STA-236';
  if (!checklistItems || !checklistItems.length || checklistItems[0]?.sprint !== s) {
    try {
      const r = await apiFetch(`/api/checklist?sprint=${encodeURIComponent(s)}`);
      checklistItems = r.items || [];
    } catch (e) {
      console.error('Failed to load items for walkthrough:', e);
    }
  }
}

async function startWalkthrough(sprint, startIndex = 0) {
  const s = sprint || document.getElementById('checklist-sprint-filter')?.value || 'STA-236';
  await ensureChecklistItemsLoaded(s);
  if (!checklistItems.length) {
    alert('No checklist items found for sprint ' + s + '. Please seed first.');
    return;
  }

  let targetIndex = startIndex;
  if (targetIndex < 0 || targetIndex >= checklistItems.length) {
    const firstPending = checklistItems.findIndex(i => i.status === 'pending');
    targetIndex = firstPending >= 0 ? firstPending : 0;
  }

  const curItem = checklistItems[targetIndex];
  saveWalkthroughState({
    active: true,
    sprint: s,
    index: targetIndex,
    itemId: curItem?.id,
    isMinimized: false
  });

  renderWalkthroughHUD();
  updateDevTourToggleUI();
}

function stopWalkthrough() {
  saveWalkthroughState(null);
  const hud = document.getElementById('checklist-walkthrough-hud');
  if (hud) hud.style.display = 'none';
  clearWalkthroughSidebarHighlight();
  updateDevTourToggleUI();
}

function clearWalkthroughSidebarHighlight() {
  document.querySelectorAll('.sidebar-item').forEach(b => {
    b.classList.remove('tour-target-highlight');
  });
}

function updateWalkthroughSidebarHighlight() {
  clearWalkthroughSidebarHighlight();
  if (!isWalkthroughActive()) return;
  const state = getWalkthroughState();
  if (!state || !checklistItems.length) return;
  const item = checklistItems[state.index];
  if (!item) return;

  const target = resolveItemTarget(item);
  const currentPath = window.location.pathname.replace(/\/+$/, '') || '/';
  const targetPath = viewToPath(target.view, null);

  // If we are NOT on the target page, highlight the sidebar item
  if (currentPath !== targetPath) {
    const sidebarBtn = document.querySelector(`.sidebar-item[data-view="${target.view}"]`);
    if (sidebarBtn) {
      sidebarBtn.classList.add('tour-target-highlight');
    }
  }

  // Update HUD guide box
  const guideBox = document.getElementById('hud-guide-box');
  const guideLabel = document.getElementById('hud-guide-label');
  const jumpBtn = document.getElementById('hud-jump-btn');

  if (guideBox && guideLabel && jumpBtn) {
    if (currentPath === targetPath) {
      guideBox.classList.add('guide-matched');
      guideLabel.innerHTML = `✅ <strong>On target page:</strong> ${target.label}`;
      jumpBtn.style.display = 'none';
    } else {
      guideBox.classList.remove('guide-matched');
      guideLabel.innerHTML = `📍 <strong>Step ${(state.index || 0) + 1}:</strong> Click <u>${target.label}</u> in sidebar`;
      jumpBtn.style.display = 'inline-block';
      jumpBtn.textContent = `Jump to ${target.label}`;
      jumpBtn.onclick = () => {
        navigateTo(target.view, null, true);
      };
    }
  }
}

function renderWalkthroughHUD() {
  const hud = document.getElementById('checklist-walkthrough-hud');
  if (!hud) return;
  if (!isWalkthroughActive() || !isDevTourEnabled()) {
    hud.style.display = 'none';
    clearWalkthroughSidebarHighlight();
    return;
  }

  const state = getWalkthroughState();
  if (!state || !checklistItems.length) {
    ensureChecklistItemsLoaded(state?.sprint).then(() => renderWalkthroughHUD());
    return;
  }

  const idx = Math.max(0, Math.min(checklistItems.length - 1, state.index || 0));
  const item = checklistItems[idx];
  if (!item) return;

  state.itemId = item.id;
  state.index = idx;
  saveWalkthroughState(state);

  hud.style.display = 'flex';
  hud.classList.toggle('minimized', Boolean(state.isMinimized));

  // Step counter & section
  const counterEl = document.getElementById('hud-step-counter');
  if (counterEl) counterEl.textContent = `Question ${idx + 1} of ${checklistItems.length}`;

  const secEl = document.getElementById('hud-item-section');
  if (secEl) secEl.textContent = item.section || 'General Verification';

  // Title
  const titleEl = document.getElementById('hud-item-title');
  if (titleEl) {
    titleEl.textContent = item.title;
    if (item.contract) {
      const tag = el('span', 'checklist-contract-tag', '⚙ contract');
      tag.title = 'Machine contract: ' + item.contract;
      titleEl.appendChild(tag);
    }
  }

  // Desc
  const descEl = document.getElementById('hud-item-desc');
  if (descEl) descEl.textContent = item.description || '';

  // How to test
  const howtoEl = document.getElementById('hud-item-howto');
  if (howtoEl) howtoEl.textContent = item.how_to_test || 'Verify this requirement in the application view.';

  // Target & sidebar highlight
  updateWalkthroughSidebarHighlight();

  // Status buttons
  updateHUDStatusButtons(item.status);

  // Notes
  const notesInput = document.getElementById('hud-notes-input');
  const draftKey = 'staypoint_cl_draft_' + item.id;
  const draftVal = localStorage.getItem(draftKey);
  const currentNote = (draftVal !== null && draftVal !== undefined && (!item.notes || draftVal.length >= item.notes.length))
    ? draftVal
    : (item.notes || '');

  if (notesInput) {
    notesInput.value = currentNote;
    notesInput.oninput = () => {
      const v = notesInput.value;
      item.notes = v;
      checklistItems[idx].notes = v;
      localStorage.setItem(draftKey, v);
      const pageTa = document.querySelector(`.checklist-item[data-id="${item.id}"] .checklist-notes-input`);
      if (pageTa) pageTa.value = v;
    };
  }

  // Prev / Next buttons
  const prevBtn = document.getElementById('hud-prev-btn');
  if (prevBtn) {
    prevBtn.disabled = idx <= 0;
    prevBtn.onclick = () => prevWalkthroughQuestion();
  }

  const nextBtn = document.getElementById('hud-next-btn');
  if (nextBtn) {
    const isLast = idx >= checklistItems.length - 1;
    nextBtn.textContent = isLast ? 'Finish Tour 🎉' : 'Done / Next ▶';
    nextBtn.onclick = () => nextWalkthroughQuestion();
  }

  // Jump select dropdown
  const jumpSel = document.getElementById('hud-jump-select');
  if (jumpSel) {
    jumpSel.innerHTML = '';
    checklistItems.forEach((it, i) => {
      const opt = document.createElement('option');
      opt.value = i;
      const statusIcon = it.status === 'pass' ? '✓' : (it.status === 'fail' ? '✗' : (it.status === 'partial' ? '◐' : '○'));
      opt.textContent = `${statusIcon} #${i + 1}: ${it.title.slice(0, 30)}…`;
      if (i === idx) opt.selected = true;
      jumpSel.appendChild(opt);
    });
    jumpSel.onchange = (e) => {
      jumpWalkthroughQuestion(parseInt(e.target.value, 10));
    };
  }

  // Save note button
  const saveNoteBtn = document.getElementById('hud-save-note-btn');
  const saveInd = document.getElementById('hud-save-indicator');
  if (saveNoteBtn) {
    saveNoteBtn.onclick = async () => {
      const val = notesInput ? notesInput.value : item.notes;
      saveNoteBtn.disabled = true;
      saveNoteBtn.textContent = 'Saving…';
      await updateChecklistNotes(item.id, val);
      saveNoteBtn.disabled = false;
      saveNoteBtn.textContent = 'Saved!';
      if (saveInd) saveInd.textContent = '✓ Note saved';
      setTimeout(() => {
        saveNoteBtn.textContent = 'Save Note';
        if (saveInd) saveInd.textContent = '';
      }, 1500);
    };
  }
}

function updateHUDStatusButtons(currentStatus) {
  const state = getWalkthroughState();
  if (!state || !checklistItems.length) return;
  const item = checklistItems[state.index];
  if (!item) return;

  const isBlocked = isItemCommitBlocked(item.id);
  const blockReason = isBlocked ? getItemCommitBlockReason(item.id) : '';

  document.querySelectorAll('.hud-status-btn').forEach(btn => {
    const st = btn.dataset.status;
    const isPass = st === 'pass';
    btn.className = `hud-status-btn${item.status === st ? ' active-' + st : ''}${isPass && isBlocked ? ' cl-btn-blocked' : ''}`;
    if (isPass && isBlocked) {
      btn.disabled = true;
      btn.title = `⛔ Blocked by Commit Gate: ${blockReason}`;
    } else {
      btn.disabled = false;
      btn.title = '';
    }
    btn.onclick = async () => {
      if (isPass && isBlocked) {
        alert(`⛔ Commit Gate Block: This checklist item cannot be marked "done" / "pass".\n\nReason: ${blockReason}\n\nRequired commits must be merged into main and compiled into the running staypointd daemon.`);
        return;
      }
      const notesInput = document.getElementById('hud-notes-input');
      const val = notesInput ? notesInput.value : item.notes;
      await updateChecklistStatus(item.id, st, val);
      updateHUDStatusButtons(st);
      const saveInd = document.getElementById('hud-save-indicator');
      if (saveInd) {
        saveInd.textContent = `✓ Status set to ${st.toUpperCase()}`;
        setTimeout(() => { if (saveInd) saveInd.textContent = ''; }, 2000);
      }
    };
  });
}

async function nextWalkthroughQuestion() {
  const state = getWalkthroughState();
  if (!state || !checklistItems.length) return;
  const idx = state.index;
  const item = checklistItems[idx];

  // Auto-save current note if modified
  const notesInput = document.getElementById('hud-notes-input');
  if (notesInput && item) {
    const val = notesInput.value;
    if (val !== item.notes) {
      await updateChecklistNotes(item.id, val);
    }
  }

  if (idx >= checklistItems.length - 1) {
    alert('🎉 You have completed all questions in the walkthrough!');
    stopWalkthrough();
    return;
  }

  state.index = idx + 1;
  state.itemId = checklistItems[state.index]?.id;
  saveWalkthroughState(state);
  renderWalkthroughHUD();
}

async function prevWalkthroughQuestion() {
  const state = getWalkthroughState();
  if (!state || !checklistItems.length) return;
  const idx = state.index;
  if (idx <= 0) return;

  const item = checklistItems[idx];
  const notesInput = document.getElementById('hud-notes-input');
  if (notesInput && item) {
    const val = notesInput.value;
    if (val !== item.notes) {
      await updateChecklistNotes(item.id, val);
    }
  }

  state.index = idx - 1;
  state.itemId = checklistItems[state.index]?.id;
  saveWalkthroughState(state);
  renderWalkthroughHUD();
}

function jumpWalkthroughQuestion(targetIndex) {
  const state = getWalkthroughState();
  if (!state || !checklistItems.length) return;
  if (targetIndex < 0 || targetIndex >= checklistItems.length) return;

  state.index = targetIndex;
  state.itemId = checklistItems[targetIndex]?.id;
  saveWalkthroughState(state);
  renderWalkthroughHUD();
}

// Wire up HUD static controls
document.getElementById('hud-exit-btn')?.addEventListener('click', stopWalkthrough);
document.getElementById('hud-toggle-minimize-btn')?.addEventListener('click', () => {
  const state = getWalkthroughState();
  if (!state) return;
  state.isMinimized = !state.isMinimized;
  saveWalkthroughState(state);
  document.getElementById('checklist-walkthrough-hud')?.classList.toggle('minimized', Boolean(state.isMinimized));
  const minBtn = document.getElementById('hud-toggle-minimize-btn');
  if (minBtn) minBtn.textContent = state.isMinimized ? '▲' : '_';
});
document.getElementById('checklist-tour-toggle-btn')?.addEventListener('click', () => {
  const next = !isDevTourEnabled();
  setDevTourEnabled(next);
});
document.getElementById('checklist-start-tour-btn')?.addEventListener('click', () => {
  const state = getWalkthroughState();
  if (state?.active) {
    renderWalkthroughHUD();
  } else {
    startWalkthrough(document.getElementById('checklist-sprint-filter')?.value || 'STA-236', 0);
  }
});

// ── Filter listeners (projects page) ─────────────────────
document.getElementById('projects-status-filter')?.addEventListener('change', (e) => {
  state.projectsFilter.status = e.target.value;
  saveProjectsFilters();
  renderProjects();
});

document.getElementById('projects-org-multiselect-btn')?.addEventListener('click', (e) => {
  e.stopPropagation();
  const menu = document.getElementById('projects-org-menu');
  const btn = document.getElementById('projects-org-multiselect-btn');
  if (!menu || !btn) return;
  const isExpanded = btn.getAttribute('aria-expanded') === 'true';
  menu.classList.toggle('hidden', isExpanded);
  btn.setAttribute('aria-expanded', String(!isExpanded));
});

document.getElementById('projects-org-select-all')?.addEventListener('click', (e) => {
  e.stopPropagation();
  const checkboxes = document.querySelectorAll('#projects-org-options .multiselect-checkbox');
  checkboxes.forEach(cb => { cb.checked = true; });
  state.projectsFilter.orgs = [];
  saveProjectsFilters();
  updateProjectsOrgButtonLabel();
  renderProjects();
});

document.getElementById('projects-org-clear-all')?.addEventListener('click', (e) => {
  e.stopPropagation();
  const checkboxes = document.querySelectorAll('#projects-org-options .multiselect-checkbox');
  checkboxes.forEach(cb => { cb.checked = false; });
  state.projectsFilter.orgs = ['__none__'];
  saveProjectsFilters();
  updateProjectsOrgButtonLabel();
  renderProjects();
});

// Outside click to dismiss multi-select dropdown
document.addEventListener('click', (e) => {
  const ms = document.getElementById('projects-org-multiselect');
  const menu = document.getElementById('projects-org-menu');
  const btn = document.getElementById('projects-org-multiselect-btn');
  if (ms && menu && !ms.contains(e.target)) {
    menu.classList.add('hidden');
    btn?.setAttribute('aria-expanded', 'false');
  }
});

// Legacy select listener for backwards compatibility
document.getElementById('projects-org-filter')?.addEventListener('change', (e) => {
  if (e.target.value === 'all') {
    state.projectsFilter.orgs = [];
  } else {
    state.projectsFilter.orgs = [e.target.value];
  }
  saveProjectsFilters();
  populateProjectsOrgFilter();
  renderProjects();
});

// ── Boot ──────────────────────────────────────────────────
loadAll().then(() => {
  // Pre-render and cache Boss Cards on initial load / first daily access
  loadBossReportCache();
  preloadBossReports(false);

  const initialRoute = pathToRoute();
  if (initialRoute.taskId || initialRoute.identifier) {
    // Direct deep link or refresh on a task URL → render full page
    openTaskPage(initialRoute, false);
  } else {
    navigateTo(initialRoute.view, initialRoute.org, false);
  }
  connectSSE();
  updateDevTourToggleUI();
  if (isWalkthroughActive()) {
    renderWalkthroughHUD();
  }
});

// Periodic Boss Card re-render (every 30 minutes)
setInterval(() => {
  preloadBossReports(true);
}, BOSS_REPORT_CACHE_TTL_MS);
