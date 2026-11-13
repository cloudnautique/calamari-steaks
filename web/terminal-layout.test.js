import test from 'node:test';
import assert from 'node:assert/strict';
import { fittedDimensions } from './terminal-layout.js';
const pane = { owned:true,connected:true,visible:true,width:800,height:400,proposal:{cols:92,rows:22} };
test('hidden, collapsed, detached and observer views never resize the shared terminal', () => {
  for (const change of [{visible:false},{width:0},{height:0},{owned:false},{connected:false},{proposal:undefined}]) {
    assert.equal(fittedDimensions({...pane,...change}),undefined);
  }
});
test('valid visible dimensions fit within supported bounds', () => {
  assert.deepEqual(fittedDimensions(pane),{cols:92,rows:22});
  assert.deepEqual(fittedDimensions({...pane,proposal:{cols:900,rows:300}}),{cols:500,rows:200});
  assert.equal(fittedDimensions({...pane,proposal:{cols:NaN,rows:20}}),undefined);
});
