export function webURL(text){
 try{const u=new URL(text);if(!['http:','https:'].includes(u.protocol)||u.username||u.password)return;return text;}catch{}
}
export function findURLs(text){
 const links=[];
 for(const match of text.matchAll(/https?:\/\/[^\s<>"'`\x00-\x1f]+/g)){
  let value=match[0].replace(/[.,;]+$/,'');
  while(value.endsWith(')')&&value.split(')').length>value.split('(').length)value=value.slice(0,-1);
  if(webURL(value))links.push({url:value,start:match.index,end:match.index+value.length});
 }
 return links;
}
// In Ghostty 0.4.0 runtime, isWrapped means continuation FROM the previous
// line (the published type comment says the reverse). Keep this covered by
// the actual renderer fixture as well as the buffer tests.
export function logicalLine(buffer,row){
 let start=row,end=row;
 while(start>0&&row-start<32&&buffer.getLine(start)?.isWrapped)start--;
 while(end<buffer.length-1&&end-start<64&&buffer.getLine(end+1)?.isWrapped)end++;
 let text='',position;
 for(let y=start;y<=end;y++){
  const line=buffer.getLine(y);if(!line)break;
  if(y===row)position=text.length;
  text+=line.translateToString(y===end);
 }
 return {text,start,end,position};
}
export function terminalURLAt(terminal,event){
 const canvas=terminal.element?.querySelector('canvas'),renderer=terminal.renderer;
 if(!canvas||!renderer)return;
 const rect=canvas.getBoundingClientRect();
 const col=Math.floor((event.clientX-rect.left)/renderer.charWidth),screenRow=Math.floor((event.clientY-rect.top)/renderer.charHeight);
 if(col<0||col>=terminal.cols||screenRow<0||screenRow>=terminal.rows)return;
 const buffer=terminal.buffer.active,row=buffer.length-terminal.rows-Math.max(0,Math.floor(terminal.getViewportY()))+screenRow;
 const line=buffer.getLine(row);if(!line)return;
 const logical=logicalLine(buffer,row);
 const offset=logical.position+line.translateToString(false,0,col).length;
 return findURLs(logical.text).find(link=>offset>=link.start&&offset<link.end)?.url;
}
export function terminalURLs(terminal){
 const buffer=terminal.buffer.active,urls=new Set();
 for(let row=Math.max(0,buffer.length-1000);row<buffer.length;row++){
  const line=logicalLine(buffer,row);
  for(const {url} of findURLs(line.text))urls.add(url);
  row=line.end;
 }
 return [...urls].slice(-40).reverse();
}
export function installTerminalLinks(element,terminal,open,report){
 let down;
 const pointer=event=>{down={x:event.clientX,y:event.clientY};};
 const click=event=>{
  if(event.button!==0||event.shiftKey||!down||Math.hypot(event.clientX-down.x,event.clientY-down.y)>5)return;
  const url=terminalURLAt(terminal,event);if(!url)return;
  event.preventDefault();event.stopImmediatePropagation();Promise.resolve(open(url)).catch(report);
 };
 element.addEventListener('mousedown',pointer,true);element.addEventListener('click',click,true);
 return()=>{element.removeEventListener('mousedown',pointer,true);element.removeEventListener('click',click,true);};
}
