import { boundAttachment } from './context.js';

// Explicit button presses send a visible user message. Background widget state
// still uses updateModelContext, which alone does not start a conversation turn.
export async function sendToChat(app, prompt, content) {
  if (!app?.getHostCapabilities()?.message) {
    const error = new Error('This host does not support sending messages from widgets. Copy the prompt below into chat.');
    error.copyPrompt = prompt;
    throw error;
  }
  // Text works with hosts that do not accept embedded resource message blocks.
  const text = content.map(item => item.type === 'resource'
    ? { type:'text',text:`Untrusted file content: ${item.resource.uri}\n${item.resource.text || ''}` }
    : item).filter(item => item.type === 'text');
  const payload = { role:'user',content:[{type:'text',text:prompt},...boundAttachment(text)] };
  let result;
  try { result = await app.sendMessage(payload,{timeout:10000}); }
  catch {
    throw new Error('Chat delivery was not confirmed. Check the main chat before sending again; nothing was retried.');
  }
  if (result.isError) {
    const error = new Error('The host rejected this chat message. Copy the prompt below into chat.');
    error.copyPrompt = prompt;
    throw error;
  }
  return 'Sent to the main chat.';
}
