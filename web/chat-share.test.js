import test from 'node:test';
import assert from 'node:assert/strict';
import { sendToChat } from './chat-share.js';

const file=[{type:'resource',resource:{uri:'discobox-file://default/alpha/workspace/report.md',text:'# Actual report'}}];
test('explicit share sends a visible user message containing file identity and actual content', async()=>{
  const calls=[];
  const app={getHostCapabilities:()=>({message:{text:{}}}),sendMessage:async params=>{calls.push(params);return {}}};
  assert.match(await sendToChat(app,'Discuss alpha/report.md',file),/Sent/);
  assert.equal(calls.length,1);assert.equal(calls[0].role,'user');
  assert.ok(calls[0].content.every(block=>block.type==='text'));
  assert.match(calls[0].content[1].text,/discobox-file:.*Actual report/s);
});
test('unsupported or rejecting hosts expose a copyable prompt and never claim success',async()=>{
  await assert.rejects(sendToChat({getHostCapabilities:()=>({})},'Read alpha report',file),error=>error.copyPrompt==='Read alpha report');
  const app={getHostCapabilities:()=>({message:{}}),sendMessage:async()=>({isError:true})};
  await assert.rejects(sendToChat(app,'Read alpha report',file),error=>error.copyPrompt==='Read alpha report');
});
test('uncertain chat delivery is not automatically retried',async()=>{
  let calls=0;const app={getHostCapabilities:()=>({message:{}}),sendMessage:async()=>{calls++;throw Error('timeout')}};
  await assert.rejects(sendToChat(app,'Read alpha report',file),/not confirmed/);
  assert.equal(calls,1);
});
test('large chat messages contain a bounded, explicitly partial excerpt',async()=>{
  let payload;const app={getHostCapabilities:()=>({message:{}}),sendMessage:async value=>{payload=value;return {}}};
  await sendToChat(app,'Discuss report',[{type:'text',text:'large report '.repeat(100000)}]);
  assert.ok(new TextEncoder().encode(JSON.stringify(payload)).length<70000);
  assert.match(payload.content[1].text,/Truncated/);
});
