import test from 'node:test';
import assert from 'node:assert/strict';
import {FilesModel,resolveFileLink,parentDirectory} from './files-model.js';
const result=(boxId,path,text='report',version='v1')=>({boxId,file:{path,uri:`discobox-file://default/${boxId}${path}`,name:'report.md',mimeType:'text/markdown',version,size:text.length},text,nextOffset:text.length,truncated:false});
test('late file reads never overwrite a different box selection',async()=>{
 let finish;const pending=new Promise(resolve=>finish=resolve);
 const model=new FilesModel(async(name,args)=>args.boxId==='a'?pending:result('b',args.path,'B'));
 model.bind('a');const old=model.open('/workspace/a.md');model.bind('b');await model.open('/workspace/b.md');finish(result('a','/workspace/a.md','A'));await old;
 assert.equal(model.state.boxId,'b');assert.equal(model.state.text,'B');assert.equal(model.state.file.path,'/workspace/b.md');
});
test('file version changes automatically update the selected preview',async()=>{
 let version='v1';const model=new FilesModel(async(name,args)=>name==='file_stat'?{version}:result(args.boxId,args.path,version,version));model.bind('a');await model.open('/workspace/report.md');version='v2';await model.checkVersion();assert.equal(model.state.text,'v2');assert.equal(model.state.file.version,'v2');
});
test('relative links stay in their box and executable schemes are refused',()=>{
 const uri='discobox-file://default/box/workspace/report.md';assert.deepEqual(resolveFileLink('notes.md',uri),{path:'/workspace/notes.md',hash:''});assert.deepEqual(resolveFileLink('/workspace/other.md',uri),{path:'/workspace/other.md',hash:''});assert.equal(resolveFileLink('javascript:alert(1)',uri),null);assert.equal(resolveFileLink('discobox-file://default/other/workspace/file.md',uri),null);assert.equal(resolveFileLink('//attacker/x',uri),null);assert.deepEqual(resolveFileLink('https://example.org',uri),{external:'https://example.org/'});assert.equal(parentDirectory('/workspace/report.md'),'/workspace');
});

test('chunked UTF-8 preview preserves a multibyte character across byte ranges',async()=>{
 const bytes=new TextEncoder().encode('A✓B');let count=0;
 const model=new FilesModel(async()=>({boxId:'a',file:{path:'/workspace/report.md',uri:'discobox-file://default/a/workspace/report.md',name:'report.md',mimeType:'text/markdown',version:'v1',size:bytes.length},blob:btoa(String.fromCharCode(...(count++?bytes.slice(2):bytes.slice(0,2)))),nextOffset:count===1?2:bytes.length,truncated:count===1}));
 model.bind('a');await model.open('/workspace/report.md');assert.equal(model.state.text,'A');await model.more();assert.equal(model.state.text,'A✓B');
});

test('directory paging appends entries and preserves the selected box',async()=>{
 const model=new FilesModel(async(name,args)=>{const start=args.offset||0;return{boxId:args.boxId,path:args.path,entries:Array.from({length:start===0?100:20},(_,i)=>({path:'/workspace/f'+(start+i),name:'f'+(start+i),type:'file'})),nextOffset:start===0?100:120,hasMore:start===0,total:120}});
 model.bind('a');await model.list('/workspace');assert.equal(model.state.entries.length,100);await model.moreEntries();assert.equal(model.state.entries.length,120);assert.equal(model.state.hasMoreEntries,false);assert.equal(model.state.boxId,'a');
});
test('version polling clears a transient error after the connection recovers',async()=>{
 let fail=false;const model=new FilesModel(async(name,args)=>{if(name==='file_stat'){if(fail)throw Error('offline');return{version:'v1'}}return result(args.boxId,args.path)});
 model.bind('a');await model.open('/workspace/report.md');fail=true;await model.checkVersion();assert.match(model.state.error,/update check failed/);fail=false;await model.checkVersion();assert.equal(model.state.error,'');assert.equal(model.state.text,'report');
});
