import test from 'node:test';import assert from 'node:assert/strict';
import {DesktopInputGate} from './desktop-input.js';
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));
test('the first double-click survives an asynchronous focus claim in order',async()=>{
 let resolve,ready=false;const delivered=[];
 const gate=new DesktopInputGate({ready:()=>ready,claim:()=>new Promise(r=>{resolve=r;}),current:()=>true,deliver:e=>delivered.push(e.type),report:e=>{throw e;}});
 for(const type of ['mousedown','mouseup','mousedown','mouseup'])assert.equal(gate.capture({type}),true);
 await tick();assert.equal(delivered.length,0);ready=true;resolve();await tick();
 assert.deepEqual(delivered,['mousedown','mouseup','mousedown','mouseup']);assert.equal(gate.capture({type:'mousedown'}),false);
});
test('blur and failed ownership discard first clicks instead of replaying them later',async()=>{
 for(const fail of [false,true]){
  let resolve;const delivered=[],errors=[];
  const gate=new DesktopInputGate({ready:()=>false,claim:()=>fail?Promise.reject(Error('denied')):new Promise(r=>resolve=r),current:()=>false,deliver:e=>delivered.push(e),report:e=>errors.push(e)});
  gate.capture({type:'mousedown'});await tick();if(!fail){gate.cancel();resolve();}await tick();
  assert.equal(delivered.length,0);assert.equal(errors.length,fail?1:0);
 }
});
