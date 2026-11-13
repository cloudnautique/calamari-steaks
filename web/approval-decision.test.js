import test from 'node:test';
import assert from 'node:assert/strict';
import {approvalPlan,decisionSummary} from './approval-decision.js';
const review={reviewToken:'private-review',request:{id:'sreq-a',sandboxId:'box-a',status:'pending',use:{add:false,routes:[{hosts:["api.example"]}]},delegate:{}},credentials:[{id:'secret-a',name:'GitHub',maxBindingTTLSeconds:7200}]};
test('both use and delegation require explicit decisions and the confirmation names the exact target',()=>{
 assert.throws(()=>approvalPlan(review,{use:'approve',secretId:'secret-a',ttlSeconds:3600}),/each requested part/);
 const plan=approvalPlan(review,{use:'approve',delegate:'deny',secretId:'secret-a',ttlSeconds:3600});
 assert.equal(plan.delegate,'deny');assert.equal(plan.use,'approve');
 const summary=decisionSummary(review.request,plan,review.credentials);
 assert.match(summary,/Box box-a/);assert.match(summary,/Pass to workers: deny/);assert.match(summary,/replace existing/);assert.match(summary,/requested routes only/);
});
test('denial needs no credential or new access and never synthesizes an approval',()=>{
 const plan=approvalPlan({...review,credentials:[]},{use:'deny',delegate:'deny'});
 assert.deepEqual(plan,{reviewToken:'private-review',use:'deny',delegate:'deny'});
});
test('settled, expired and unavailable credential reviews cannot become approvals',()=>{
 for(const status of ['approved','denied'])assert.throws(()=>approvalPlan({...review,request:{...review.request,status}},{use:'approve',delegate:'approve'}),/settled/);
 assert.throws(()=>approvalPlan({...review,reviewToken:''},{}),/refreshed/);
 assert.throws(()=>approvalPlan(review,{use:'approve',delegate:'deny',secretId:'unavailable',ttlSeconds:3600}),/available credential/);
 for(const ttl of [0,-1,7201,2592001,Infinity])assert.throws(()=>approvalPlan(review,{use:'approve',delegate:'deny',secretId:'secret-a',ttlSeconds:ttl}));
});
