// ==UserScript==
// @name         StayPoint Dev Overlay
// @namespace    https://github.com/VinnyVanGogh/staypoint
// @version      1.0.0
// @description  Draggable StayPoint task/checklist overlay on any webpage
// @match        *://*/*
// @grant        GM_xmlhttpRequest
// @grant        GM_getValue
// @grant        GM_setValue
// @connect      127.0.0.1
// ==/UserScript==

(function () {
  'use strict';

  // ── Config ──────────────────────────────────────────────────────────────────
  const BASE_URL = GM_getValue('sp_base_url', 'http://127.0.0.1:3100');
  const TOKEN    = GM_getValue('sp_token', '');          // set via overlay config panel
  const POLL_MS  = 8000;

  // ── State ────────────────────────────────────────────────────────────────────
  let panel, pollTimer, dragging = false, dragOffX = 0, dragOffY = 0;
  const POS_KEY = 'sp_overlay_pos';

  // ── Utils ────────────────────────────────────────────────────────────────────
  function api(method, path, body) {
    return new Promise((resolve, reject) => {
      GM_xmlhttpRequest({
        method,
        url: BASE_URL + path,
        headers: {
          'Authorization': 'Bearer ' + TOKEN,
          'Content-Type': 'application/json',
        },
        data: body ? JSON.stringify(body) : undefined,
        onload(r) {
          if (r.status >= 200 && r.status < 300) {
            try { resolve(JSON.parse(r.responseText)); } catch { resolve({}); }
          } else {
            reject(new Error(r.status + ' ' + r.responseText));
          }
        },
        onerror(e) { reject(e); },
      });
    });
  }

  // ── Dragging ─────────────────────────────────────────────────────────────────
  function savePos() {
    const r = panel.getBoundingClientRect();
    GM_setValue(POS_KEY, JSON.stringify({ right: window.innerWidth - r.right, top: r.top }));
  }

  function restorePos() {
    try {
      const p = JSON.parse(GM_getValue(POS_KEY, 'null'));
      if (p) {
        panel.style.right = Math.max(0, p.right) + 'px';
        panel.style.top   = Math.max(0, p.top)   + 'px';
        panel.style.left  = 'auto';
      }
    } catch { /* keep defaults */ }
  }

  function initDrag(handle) {
    handle.addEventListener('mousedown', (e) => {
      dragging = true;
      const r = panel.getBoundingClientRect();
      dragOffX = e.clientX - r.left;
      dragOffY = e.clientY - r.top;
      e.preventDefault();
    });
    document.addEventListener('mousemove', (e) => {
      if (!dragging) return;
      panel.style.left  = (e.clientX - dragOffX) + 'px';
      panel.style.right = 'auto';
      panel.style.top   = (e.clientY - dragOffY) + 'px';
    });
    document.addEventListener('mouseup', () => {
      if (dragging) { dragging = false; savePos(); }
    });
  }

  // ── Render helpers ───────────────────────────────────────────────────────────
  function statusDot(status) {
    const colors = {
      active: '#4ade80', in_progress: '#4ade80', in_review: '#facc15',
      done: '#94a3b8', blocked: '#f87171', todo: '#60a5fa',
    };
    const c = colors[status] || '#94a3b8';
    return `<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:${c};margin-right:6px;flex-shrink:0"></span>`;
  }

  function checkIcon(status) {
    if (status === 'pass')  return '<span style="color:#4ade80;font-size:11px;margin-right:4px">✓</span>';
    if (status === 'fail')  return '<span style="color:#f87171;font-size:11px;margin-right:4px">✗</span>';
    if (status === 'skip')  return '<span style="color:#94a3b8;font-size:11px;margin-right:4px">—</span>';
    return '<span style="color:#94a3b8;font-size:11px;margin-right:4px">○</span>';
  }

  function escHtml(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // ── Render ───────────────────────────────────────────────────────────────────
  async function refresh() {
    if (!TOKEN) { renderError('No token — click ⚙ to configure'); return; }

    try {
      const [tasksRes, checkRes] = await Promise.all([
        api('GET', '/api/tasks?status=in_progress'),
        api('GET', '/api/checklist'),
      ]);

      const tasks    = Array.isArray(tasksRes) ? tasksRes : (tasksRes.tasks || []);
      const items    = Array.isArray(checkRes)  ? checkRes  : (checkRes.items || []);
      const active   = tasks.filter(t => t.status === 'in_progress' || t.status === 'active');
      const pending  = items.filter(i => i.status === 'pending' || i.status === 'not_done');
      const passed   = items.filter(i => i.status === 'pass');
      const failed   = items.filter(i => i.status === 'fail');

      renderContent(active, items, pending, passed, failed);
    } catch (err) {
      renderError('API error: ' + escHtml(err.message));
    }
  }

  function renderError(msg) {
    const body = panel.querySelector('#sp-body');
    if (body) body.innerHTML = `<div style="color:#f87171;font-size:11px;padding:8px">${msg}</div>`;
  }

  function renderContent(active, items, pending, passed, failed) {
    const body = panel.querySelector('#sp-body');
    if (!body) return;

    // Active tasks section
    let taskHtml = '';
    if (active.length === 0) {
      taskHtml = '<div style="color:#94a3b8;font-size:11px;padding:4px 0">No active tasks</div>';
    } else {
      taskHtml = active.map(t => `
        <div style="margin-bottom:6px;padding:6px;background:#1e293b;border-radius:4px">
          <div style="display:flex;align-items:center;font-size:11px;font-weight:600;color:#e2e8f0">
            ${statusDot(t.status)}${escHtml(t.name || t.title || t.id)}
          </div>
          ${t.organization ? `<div style="font-size:10px;color:#64748b;margin-top:2px;padding-left:14px">${escHtml(t.organization)}${t.project ? ' / ' + escHtml(t.project) : ''}</div>` : ''}
          <div style="display:flex;gap:4px;margin-top:6px;padding-left:14px">
            <button class="sp-btn" data-action="done" data-id="${escHtml(t.id)}" style="font-size:10px;padding:2px 6px;background:#16a34a;border:none;border-radius:3px;color:#fff;cursor:pointer">Done</button>
            <button class="sp-btn" data-action="comment" data-id="${escHtml(t.id)}" style="font-size:10px;padding:2px 6px;background:#2563eb;border:none;border-radius:3px;color:#fff;cursor:pointer">Comment</button>
          </div>
        </div>`).join('');
    }

    // Checklist summary
    const passCount  = passed.length;
    const failCount  = failed.length;
    const totalCount = items.length;
    const pct = totalCount > 0 ? Math.round(passCount / totalCount * 100) : 0;
    const barColor = failCount > 0 ? '#f87171' : (pct === 100 ? '#4ade80' : '#3b82f6');

    const checkHtml = `
      <div style="margin-bottom:6px">
        <div style="display:flex;justify-content:space-between;font-size:10px;color:#94a3b8;margin-bottom:3px">
          <span>DoD gate</span>
          <span>${passCount}/${totalCount} (${pct}%)</span>
        </div>
        <div style="background:#1e293b;border-radius:3px;height:5px">
          <div style="background:${barColor};height:5px;border-radius:3px;width:${pct}%;transition:width 0.3s"></div>
        </div>
        ${failCount > 0 ? `<div style="color:#f87171;font-size:10px;margin-top:3px">${failCount} failing</div>` : ''}
      </div>
      ${pending.slice(0, 5).map(i =>
        `<div style="display:flex;align-items:flex-start;font-size:10px;color:#cbd5e1;margin-bottom:3px">
          ${checkIcon(i.status)}<span style="flex:1">${escHtml(i.title)}</span>
        </div>`
      ).join('')}
      ${pending.length > 5 ? `<div style="font-size:10px;color:#94a3b8">+${pending.length - 5} more pending…</div>` : ''}`;

    body.innerHTML = `
      <div style="margin-bottom:10px">
        <div style="font-size:10px;font-weight:700;color:#64748b;text-transform:uppercase;letter-spacing:.05em;margin-bottom:6px">Active Tasks</div>
        ${taskHtml}
      </div>
      <div>
        <div style="font-size:10px;font-weight:700;color:#64748b;text-transform:uppercase;letter-spacing:.05em;margin-bottom:6px">Checklist</div>
        ${checkHtml}
      </div>`;

    // Wire task action buttons
    body.querySelectorAll('.sp-btn').forEach(btn => {
      btn.addEventListener('click', () => handleTaskAction(btn.dataset.action, btn.dataset.id));
    });
  }

  // ── Task actions ─────────────────────────────────────────────────────────────
  async function handleTaskAction(action, id) {
    if (action === 'done') {
      if (!confirm('Mark task done?')) return;
      try {
        await api('POST', `/api/tasks/${id}/done`);
        refresh();
      } catch (e) { alert('Error: ' + e.message); }
    } else if (action === 'comment') {
      const text = prompt('Comment text:');
      if (!text) return;
      try {
        await api('POST', `/api/tasks/${id}/comments`, { body: text, author: 'board' });
        refresh();
      } catch (e) { alert('Error: ' + e.message); }
    }
  }

  // ── Config panel ─────────────────────────────────────────────────────────────
  function showConfig() {
    const cur = { url: GM_getValue('sp_base_url', 'http://127.0.0.1:3100'), token: GM_getValue('sp_token', '') };
    const newUrl   = prompt('StayPoint base URL:', cur.url);
    if (newUrl === null) return;
    const newToken = prompt('Auth token:', cur.token);
    if (newToken === null) return;
    GM_setValue('sp_base_url', newUrl.trim() || cur.url);
    GM_setValue('sp_token',    newToken.trim());
    location.reload();
  }

  // ── Build panel ──────────────────────────────────────────────────────────────
  function buildPanel() {
    panel = document.createElement('div');
    panel.id = 'sp-overlay';
    panel.style.cssText = [
      'position:fixed',
      'top:16px',
      'right:16px',
      'z-index:2147483647',
      'width:260px',
      'background:#0f172a',
      'border:1px solid #334155',
      'border-radius:8px',
      'box-shadow:0 8px 32px rgba(0,0,0,.6)',
      'font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif',
      'color:#e2e8f0',
      'user-select:none',
    ].join(';');

    panel.innerHTML = `
      <div id="sp-header" style="display:flex;align-items:center;justify-content:space-between;padding:8px 10px;border-bottom:1px solid #1e293b;cursor:grab;background:#1e293b;border-radius:7px 7px 0 0">
        <span style="font-size:11px;font-weight:700;color:#7c3aed;letter-spacing:.03em">⬡ StayPoint</span>
        <div style="display:flex;gap:4px">
          <button id="sp-refresh" title="Refresh" style="background:none;border:none;color:#64748b;cursor:pointer;font-size:13px;padding:2px 4px;line-height:1">↻</button>
          <button id="sp-config"  title="Config"  style="background:none;border:none;color:#64748b;cursor:pointer;font-size:13px;padding:2px 4px;line-height:1">⚙</button>
          <button id="sp-dismiss" title="Dismiss" style="background:none;border:none;color:#64748b;cursor:pointer;font-size:13px;padding:2px 4px;line-height:1">✕</button>
        </div>
      </div>
      <div id="sp-body" style="padding:10px;max-height:380px;overflow-y:auto;font-size:12px">
        <div style="color:#64748b;font-size:11px">Loading…</div>
      </div>`;

    document.body.appendChild(panel);
    restorePos();
    initDrag(panel.querySelector('#sp-header'));

    panel.querySelector('#sp-dismiss').addEventListener('click', () => {
      clearInterval(pollTimer);
      panel.remove();
    });
    panel.querySelector('#sp-refresh').addEventListener('click', refresh);
    panel.querySelector('#sp-config').addEventListener('click', showConfig);
  }

  // ── Init ─────────────────────────────────────────────────────────────────────
  function init() {
    buildPanel();
    refresh();
    pollTimer = setInterval(refresh, POLL_MS);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
