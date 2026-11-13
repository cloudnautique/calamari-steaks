import test from 'node:test';
import assert from 'node:assert/strict';
import {StreamReconnect,canReconnect} from './stream-reconnect.js';
test('reconnect deduplicates failures, backs off and cancels when leaving a box',()=>{
 const timers=new Map(),delays=[];let id=0,reads=0;
 const reconnect=new StreamReconnect(()=>reads++,(fn,delay)=>{timers.set(++id,fn);delays.push(delay);return id},id=>timers.delete(id));
 reconnect.request();reconnect.request();assert.deepEqual(delays,[1000]);
 const fire=()=>{const [id,fn]=timers.entries().next().value;timers.delete(id);fn()};fire();assert.equal(reads,1);
 reconnect.request();assert.deepEqual(delays,[1000,2000]);reconnect.cancel();assert.equal(timers.size,0);
 reconnect.request();assert.equal(delays.at(-1),1000);
});
test('ended, replaced and unauthorized sessions do not silently reconnect',()=>{
 assert.equal(canReconnect(new Error('network disconnected')),true);
 assert.equal(canReconnect({sessionEnded:true}),false);
 assert.equal(canReconnect({sessionChanged:true}),false);
 assert.equal(canReconnect(new Error('Discobox returned HTTP 403')),false);
});
