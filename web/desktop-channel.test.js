import test from 'node:test';
import assert from 'node:assert/strict';
import {DesktopChannel} from './desktop-channel.js';
const idle=new Promise(()=>{});
const tick=()=>new Promise(resolve=>setTimeout(resolve,0));

test('private stream bytes reach the renderer without another network transport',async()=>{
 const calls=[],received=[];
 const channel=new DesktopChannel(async(name,args)=>{calls.push({name,args});if(name==='desktop_read')return {chunks:[btoa('RFB 003.008\n')],cursor:1,closed:true};return {ok:true}},'private-id');
 channel.onmessage=event=>received.push(new TextDecoder().decode(event.data));channel.start();await tick();
 assert.deepEqual(received,['RFB 003.008\n']);assert.equal(channel.readyState,3);
 assert.deepEqual(calls.map(call=>call.name),['desktop_read','desktop_action']);
});
test('writes coalesce, preserve order, and serialize bounded batches',async()=>{
 const writes=[];let release;
 const blocked=new Promise(resolve=>{release=resolve});
 const channel=new DesktopChannel(async(name,args)=>{if(name==='desktop_read')return idle;if(args.action==='write'){writes.push(args);if(writes.length===1)await blocked}return {ok:true}},'private-id');channel.start();
 channel.send(new TextEncoder().encode('hello'));const done=channel.flush();
 channel.send(new TextEncoder().encode(' world'));assert.equal(writes.length,1);release();await done;
 assert.equal(writes.length,2);assert.equal(atob(writes[0].data)+atob(writes[1].data),'hello world');
 assert.deepEqual(writes.map(write=>write.sequence),[1,2]);assert.equal(channel.bufferedAmount,0);channel.close();
});
test('uncertain input ends the stream without retrying or replaying pending input',async()=>{
 let writes=0;const errors=[];
 const channel=new DesktopChannel(async(name,args)=>{if(name==='desktop_read')return idle;if(args.action==='write'){writes++;throw Error('timeout')}return {}},'private-id');
 channel.onerror=event=>errors.push(event.message);channel.start();channel.send(new Uint8Array([1]));await channel.flush();await tick();
 assert.equal(writes,1);assert.equal(channel.readyState,3);assert.match(errors[0],/not confirmed/);
 assert.throws(()=>channel.send(new Uint8Array([2])),/disconnected/);
});
test('a stalled host cannot grow an unbounded desktop input queue',async()=>{
 const channel=new DesktopChannel(async(name)=>name==='desktop_read'?idle:{},'private-id');channel.start();
 channel.send(new Uint8Array(256*1024+1));assert.equal(channel.readyState,3);assert.equal(channel.bufferedAmount,0);
});

test('a coalesced 128 KiB graphics batch reaches the renderer without a fragmentation error',async()=>{
 const payload=new Uint8Array(128*1024).fill(37);let binary='';for(const value of payload)binary+=String.fromCharCode(value);
 let bytes=0;const channel=new DesktopChannel(async(name)=>name==='desktop_read'?{chunks:[btoa(binary)],cursor:50,closed:true}:{},'private-id');
 channel.onmessage=event=>{bytes+=event.data.byteLength};channel.start();await tick();assert.equal(bytes,payload.length);
});
