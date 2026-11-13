import test from 'node:test';
import assert from 'node:assert/strict';
import {FocusControl} from './focus-control.js';
test('blur during pending acquisition releases once acknowledgement arrives',async()=>{
 let unblock; const calls=[]; const pending=new Promise(r=>{unblock=r});
 const control=new FocusControl(async()=>{calls.push('claim');await pending},async()=>{calls.push('release')});
 const focus=control.focus(true);await Promise.resolve();const blur=control.focus(false);unblock();await focus;await blur;
 assert.deepEqual(calls,['claim','release']);assert.equal(control.held,false);
});
test('submission finishes before focus loss yields control',async()=>{
 const calls=[];let unblock; const pending=new Promise(r=>{unblock=r});
 const control=new FocusControl(async()=>{calls.push('claim')},async()=>{calls.push('release')});
 await control.focus(true);
 const send=control.run(async()=>{calls.push('input');await pending;calls.push('sent')});
 const blur=control.focus(false);await Promise.resolve();assert.deepEqual(calls,['claim','input']);
 unblock();await send;await blur;assert.deepEqual(calls,['claim','input','sent','release']);
});
test('failed input is never replayed and blur still releases ownership',async()=>{
 let writes=0,releases=0;
 const control=new FocusControl(async()=>{},async()=>{releases++});await control.focus(true);
 await assert.rejects(control.run(async()=>{writes++;throw Error('uncertain')}));await control.focus(false);
 assert.equal(writes,1);assert.equal(releases,1);
});
