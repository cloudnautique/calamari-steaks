import test from 'node:test';
import assert from 'node:assert/strict';
import { ContextSync } from './context.js';
test('context preserves launch receipt when selection changes and serializes updates', async () => {
  const calls = []; let release;
  const wait = new Promise(resolve => { release = resolve; });
  const sync = new ContextSync(async params => { calls.push(params); if (calls.length === 1) await wait; }, () => {});
  const first = sync.update({ activeBoxId: 'alpha' }, 'box_launched', { boxId:'alpha',task:'test' });
  const second = sync.update({ activeBoxId: 'beta' }, 'box_opened', { boxId:'beta' });
  await Promise.resolve(); assert.equal(calls.length,1); release(); await first; await second;
  const value = JSON.parse(calls[1].content[0].text.split('\n')[1]);
  assert.equal(value.activeBoxId,'beta'); assert.equal(value.recentWidgetActions[0].boxId,'alpha');
});
test('host rejection does not poison later context or retry user actions', async () => {
  let count = 0; const statuses = [];
  const sync = new ContextSync(async () => { if (++count === 1) throw new Error('unsupported'); }, message => statuses.push(message));
  await sync.update({},'message_sent',{boxId:'alpha'});
  await sync.update({},'box_opened',{boxId:'beta'});
  assert.match(statuses[0],/unavailable/); assert.match(statuses[1],/shared/); assert.equal(count,2);
});
