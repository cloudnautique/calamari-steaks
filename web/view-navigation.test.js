import test from'node:test';import assert from'node:assert/strict';import{viewNavigation}from'./view-navigation.js';
test('assistant view selection is pinned to the existing box and leaves human control alone',()=>{
 assert.deepEqual(viewNavigation({boxId:'a',view:'files',filePath:'/workspace/report.md'},'a',false),{boxId:'a',view:'files',filePath:'/workspace/report.md'});
 assert.throws(()=>viewNavigation({boxId:'a',view:'desktop'},'a',true),/left unchanged/);
 assert.throws(()=>viewNavigation({boxId:'b',view:'terminal'},'a',false),/another box/);
 assert.throws(()=>viewNavigation({boxId:'a',view:'other'},'a',false),/Choose/);
 assert.throws(()=>viewNavigation({boxId:'a',view:'files',filePath:'relative'},'a',false),/absolute/);
});
