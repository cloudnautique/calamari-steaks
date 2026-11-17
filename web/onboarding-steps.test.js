import test from 'node:test';
import assert from 'node:assert/strict';
import { stepRows } from './onboarding-steps.js';

test('diagnosis rows keep layer order, drop skipped layers and show hints only on problems', () => {
  const rows = stepRows([
    { layer: 'address', status: 'ok', summary: 'discobox://d1-…', hint: 'unused' },
    { layer: 'relay', status: 'warn', summary: 'no relay', hint: 'Check relay URLs' },
    { layer: 'admission', status: 'failed', summary: 'not enrolled', hint: 'Run the enrollment command' },
    { layer: 'server', status: 'skipped' },
  ]);
  assert.deepEqual(rows.map(row => row.label), ['Address', 'Relay', 'Admission']);
  assert.equal(rows[0].hint, '');
  assert.equal(rows[2].hint, 'Run the enrollment command');
  assert.equal(rows[2].mark, '✗');
  assert.deepEqual(stepRows(undefined), []);
});
