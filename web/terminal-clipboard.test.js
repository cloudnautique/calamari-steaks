import test from 'node:test';import assert from 'node:assert/strict';
import{clipboardShortcut,checkedPaste,MAX_PASTE_BYTES}from'./terminal-clipboard.js';
test('clipboard shortcuts bypass terminal encoding even when native events omit physical key codes',()=>{
 assert.equal(clipboardShortcut({key:'v',code:'',metaKey:true}),'paste');
 assert.equal(clipboardShortcut({key:'V',ctrlKey:true,shiftKey:true}),'paste');
 assert.equal(clipboardShortcut({key:'c',metaKey:true}),'copy');
 assert.equal(clipboardShortcut({key:'c',ctrlKey:true}),undefined);
 assert.equal(clipboardShortcut({key:'v',ctrlKey:true,altKey:true}),undefined);
});
test('paste preserves text without adding Enter and is bounded before any transmission',()=>{
 const text='first line\nsecond line ✓';assert.equal(checkedPaste(text),text);
 assert.throws(()=>checkedPaste(''),/no clipboard text/);
 assert.equal(checkedPaste('a'.repeat(MAX_PASTE_BYTES)).length,MAX_PASTE_BYTES);
 assert.throws(()=>checkedPaste('✓'.repeat(MAX_PASTE_BYTES)),/Nothing was sent/);
});
