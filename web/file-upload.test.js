import test from 'node:test';import assert from 'node:assert/strict';
import {uploadFile,MAX_UPLOAD_SIZE} from './file-upload.js';
const file=(text,name='report.txt')=>Object.assign(new Blob([text]),{name});
test('file bytes go only to bounded private chunks, then commit the pinned destination',async()=>{
 const calls=[],data='secret✓'.repeat(10000);let bytes=[];
 const result=await uploadFile(async(name,args)=>{
  assert.equal(name,'file_upload');calls.push(args);
  if(args.action==='begin')return {uploadId:'private',chunkSize:32*1024};
  if(args.action==='chunk'){bytes.push(Buffer.from(args.data,'base64'));return {offset:args.offset+Buffer.from(args.data,'base64').length};}
  return {file:{path:'/workspace/report.txt'}};
 },file(data),{boxId:'box',directory:'/workspace'});
 assert.equal(Buffer.concat(bytes).toString(),data);assert.equal(calls.at(-1).action,'finish');
 assert.equal(calls[0].path,'/workspace/report.txt');assert.equal(result.path,'/workspace/report.txt');
 assert.ok(calls.filter(c=>c.action==='chunk').every(c=>!c.boxId&&!c.path));
});
test('changed destination cancels without committing and uncertain chunks are never retried',async()=>{
 for(const fail of [false,true]){
  const actions=[];let current=true;
  await assert.rejects(uploadFile(async(_,args)=>{actions.push(args.action);if(args.action==='begin')return {uploadId:'private',chunkSize:10};if(args.action==='chunk'){if(fail)throw Error('lost response');current=false;return {offset:1};}return {};},file('x'),{boxId:'box',directory:'/workspace',current:()=>current}));
  assert.deepEqual(actions,['begin','chunk','cancel']);
 }
});
test('empty files commit and oversized or unsafe file names never begin',async()=>{
 const actions=[];
 await uploadFile(async(_,a)=>{actions.push(a.action);return a.action==='begin'?{uploadId:'x',chunkSize:10}:{file:{}};},file(''),{boxId:'box',directory:'/'});
 assert.deepEqual(actions,['begin','finish']);
 for(const f of [{name:'../secret',size:1},{name:'huge',size:MAX_UPLOAD_SIZE+1}])await assert.rejects(uploadFile(()=>{throw Error('must not call');},f,{boxId:'box',directory:'/workspace'}));
});
