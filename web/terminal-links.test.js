import test from 'node:test';import assert from 'node:assert/strict';
import {findURLs,webURL,logicalLine,terminalURLs,installTerminalLinks} from './terminal-links.js';
function buffer(rows){return {length:rows.length,getLine:y=>rows[y]&&{isWrapped:rows[y].wrapped,translateToString:()=>rows[y].text}};}
test('only HTTP(S) links can be launched; login query and fragments are preserved',()=>{
 const url='https://login.example/verify?code=abc_DEF-12&state=x%2By#continue';
 assert.equal(findURLs(`Open (${url}).`)[0].url,url);
 for(const u of ['javascript:alert(1)','file:///etc/passwd','data:text/html,x','https://user:pass@example.org'])assert.equal(webURL(u),undefined);
 assert.equal(findURLs('http://127.0.0.1:61335/')[0].url,'http://127.0.0.1:61335/');
});
test('wrapped login links reassemble locally and ordinary line breaks stay separate',()=>{
 const b=buffer([{text:'Open https://example.org/login?code=',wrapped:false},{text:'abc_DEF&state=123',wrapped:true},{text:'https://second.example/',wrapped:false}]);
 assert.equal(logicalLine(b,1).text,'Open https://example.org/login?code=abc_DEF&state=123');
 assert.deepEqual(terminalURLs({buffer:{active:b}}),['https://second.example/','https://example.org/login?code=abc_DEF&state=123']);
});

test('modifier clicks never reach the renderer link opener for non-web schemes',()=>{
 const element=new EventTarget();const opened=[];
 installTerminalLinks(element,{element:null},url=>opened.push(url),()=>{});
 let rendererActivated=0;element.addEventListener('click',()=>rendererActivated++);
 const click=(init)=>{const event=new Event('click',{cancelable:true});Object.assign(event,{button:0,clientX:1,clientY:1,shiftKey:false,ctrlKey:false,metaKey:false},init);element.dispatchEvent(event);return event};
 for(const modifier of [{ctrlKey:true},{metaKey:true}]){const event=click(modifier);assert.equal(event.defaultPrevented,true)}
 assert.equal(rendererActivated,0);assert.deepEqual(opened,[]);
 click({});assert.equal(rendererActivated,1,'plain clicks still reach the terminal');
});
