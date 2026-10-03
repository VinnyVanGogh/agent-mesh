// Unit tests for the stats-bar "STUCK" age check (STA-482, STA-517).
// Run with: node --test tests/unit/stuck-check.test.mjs
// These tests must not start a daemon or browser.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { latestRunSteps, isStuck } = require('../../internal/server/webui/lib/runsteps.js');

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

test('latestRunSteps returns only the last run group', () => {
  const steps = [
    { run_id: 'r1', kind: 'wake',  created_at: ago(SIX_MIN) },
    { run_id: 'r2', kind: 'wake',  created_at: ago(THIRTY_S) },
    { run_id: 'r2', kind: 'state', created_at: ago(THIRTY_S) },
  ];
  const latest = latestRunSteps(steps);
  assert.equal(latest.length, 2);
  assert.ok(latest.every(s => s.run_id === 'r2'));
});
