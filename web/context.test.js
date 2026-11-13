import test from 'node:test';
import assert from 'node:assert/strict';
import { ContextSync } from './context.js';
test('context preserves launch receipt when selection changes and serializes updates', async () => {
  const calls = []; let release;
  const wait = new Promise(resolve => { release = resolve; });
  const sync = new ContextSync(async params => { calls.push(params); if (calls.length === 1) await wait; }, () => {});
  const first = sync.update({ activeBoxId: 'alpha' }, 'box_launched', { boxId:'alpha',task:'test' });
  await Promise.resolve();
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

test('explicit file attachment survives later widget selection context', async()=>{const calls=[];const sync=new ContextSync(async data=>calls.push(data),()=>{});sync.setAttachment([{type:'resource',resource:{uri:'discobox-file://default/a/workspace/report.md',text:'report'}}]);await sync.update({activeBoxId:'a'},'file_shared');await sync.update({activeBoxId:'b'},'box_opened');assert.equal(calls[1].content[1].resource.text,'report');assert.equal(calls[1].content[1].resource.uri,'discobox-file://default/a/workspace/report.md');});

test('slow hosts keep one pending context update rather than a growing queue',async()=>{
 const calls=[];let unblock;const blocked=new Promise(resolve=>{unblock=resolve});
 const sync=new ContextSync(async value=>{calls.push(value);if(calls.length===1)await blocked},()=>{});
 const done=sync.update({activeBoxId:'first'},'box_opened');await Promise.resolve();
 for(let i=0;i<100;i++)sync.update({activeBoxId:String(i)},'box_opened');
 assert.equal(calls.length,1);unblock();await done;
 assert.equal(calls.length,2);assert.equal(JSON.parse(calls[1].content[0].text.split('\n')[1]).activeBoxId,'99');
});
test('large shared files become explicit excerpts within the context byte bound',async()=>{
 let published;const sync=new ContextSync(async data=>{published=data},()=>{});
 sync.setAttachment([{type:'resource',resource:{uri:'discobox-file://default/a/workspace/report.md',text:'long report\n'.repeat(100000)}}]);
 await sync.update({activeBoxId:'a'},'file_shared');
 assert.ok(new TextEncoder().encode(JSON.stringify(published)).length<80000);
 assert.match(published.content[1].text,/Truncated/);assert.match(published.content[1].text,/read_box_file/);
});
