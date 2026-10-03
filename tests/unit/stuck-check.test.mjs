// Unit tests for the stats-bar "STUCK" age check (STA-482).
// Run with: node --test tests/unit/stuck-check.test.mjs
// These tests must not start a daemon or browser.

import { test } from 'node:test';
import assert from 'node:assert/strict';

// Pure helpers mirroring app.js — keep in sync.

function groupStepsByRun(allSteps) {
  const runs = new Map();
  const order = [];
  for (const s of allSteps) {
    const rid = s.run_id || '';
    if (!runs.has(rid)) { runs.set(rid, []); order.push(rid); }
    runs.get(rid).push(s);
  }
  return order.map(rid => runs.get(rid));
}

function latestRunSteps(allSteps) {
  if (!allSteps || !allSteps.length) return [];
  const groups = groupStepsByRun(allSteps);
  return groups[groups.length - 1] || [];
}

// Mirrors the isStuck expression used in refreshTaskStatsBar / renderTaskPage.
function isStuck(allSteps, nowMs, taskStatus) {
  const latest = latestRunSteps(allSteps);
  const lastStepAt = latest.length ? new Date(latest[latest.length - 1].created_at).getTime() : null;
  return lastStepAt !== null && (nowMs - lastStepAt) > 5 * 60 * 1000 && taskStatus !== 'done';
}

// Fixed reference point so tests are deterministic.
const NOW = 1_700_000_000_000; // arbitrary epoch ms
const ago = ms => new Date(NOW - ms).toISOString();

const SIX_MIN  = 6 * 60 * 1000;
const FOUR_MIN = 4 * 60 * 1000;
const THIRTY_S = 30 * 1000;

test('no steps → not stuck', () => {
  assert.equal(isStuck([], NOW, 'in_progress'), false);
});

test('done task with stale steps → not stuck', () => {
  const steps = [{ run_id: 'r1', kind: 'checkpoint', created_at: ago(SIX_MIN) }];
  assert.equal(isStuck(steps, NOW, 'done'), false);
});

test('fresh run (only wake step 30 s ago) after stale previous run → not stuck', () => {
  // Previous run ended >5 min ago; new run just woke up.
  const prevRun = [
    { run_id: 'r-prev', kind: 'wake',       created_at: ago(SIX_MIN) },
    { run_id: 'r-prev', kind: 'checkpoint', created_at: ago(SIX_MIN) },
  ];
  const newRun = [
    { run_id: 'r-new', kind: 'wake', created_at: ago(THIRTY_S) },
  ];
  assert.equal(isStuck([...prevRun, ...newRun], NOW, 'in_progress'), false);
});

test('single run whose last step is 4 min old → not stuck', () => {
  const steps = [
    { run_id: 'r1', kind: 'wake',       created_at: ago(SIX_MIN) },
    { run_id: 'r1', kind: 'checkpoint', created_at: ago(FOUR_MIN) },
  ];
  assert.equal(isStuck(steps, NOW, 'in_progress'), false);
});

test('single run whose last step is 6 min old → stuck', () => {
  const steps = [
    { run_id: 'r1', kind: 'wake',       created_at: ago(SIX_MIN) },
    { run_id: 'r1', kind: 'checkpoint', created_at: ago(SIX_MIN) },
  ];
  assert.equal(isStuck(steps, NOW, 'in_progress'), true);
});

test('current run with recent step after an old run → not stuck', () => {
  const oldRun = [{ run_id: 'r-old', kind: 'checkpoint', created_at: ago(SIX_MIN) }];
  const curRun = [
    { run_id: 'r-cur', kind: 'wake',       created_at: ago(THIRTY_S) },
    { run_id: 'r-cur', kind: 'checkpoint', created_at: ago(THIRTY_S) },
  ];
  assert.equal(isStuck([...oldRun, ...curRun], NOW, 'in_progress'), false);
});
