export const MAX_PASTE_BYTES=65536-12;
export function clipboardShortcut(event){
 if(event.altKey)return;
 const key=String(event.key||'').toLowerCase();
 if((event.metaKey||event.ctrlKey)&&(key==='v'||event.code==='KeyV'))return 'paste';
 if((event.metaKey||event.ctrlKey&&event.shiftKey)&&(key==='c'||event.code==='KeyC'))return 'copy';
 if(event.shiftKey&&!event.ctrlKey&&!event.metaKey&&(key==='insert'||event.code==='Insert'))return 'paste';
}
export function checkedPaste(text){
 if(typeof text!=='string'||!text.length)throw Error('There is no clipboard text to paste.');
 if(new TextEncoder().encode(text).length>MAX_PASTE_BYTES)throw Error('Paste is too large; keep it below 64 KiB. Nothing was sent.');
 return text;
}
export function installTerminalClipboard(element,terminal,report){
 const keydown=event=>{
  if(!clipboardShortcut(event))return;
  // Allow the browser/host's native clipboard action, but stop Ghostty from
  // encoding the shortcut as terminal input (including CSI-u key sequences).
  event.stopImmediatePropagation();
 };
 const paste=event=>{
  event.preventDefault();event.stopImmediatePropagation();
  try{terminal.paste(checkedPaste(event.clipboardData?.getData('text/plain')||event.clipboardData?.getData('text')))}catch(error){report(error)}
 };
 const copy=event=>{
  const text=terminal.getSelection();if(!text||!event.clipboardData)return;
  event.preventDefault();event.stopImmediatePropagation();event.clipboardData.setData('text/plain',text);
 };
 element.addEventListener('keydown',keydown,true);element.addEventListener('paste',paste,true);element.addEventListener('copy',copy,true);
 return()=>{element.removeEventListener('keydown',keydown,true);element.removeEventListener('paste',paste,true);element.removeEventListener('copy',copy,true)};
}
