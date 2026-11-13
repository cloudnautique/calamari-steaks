const MAX_PENDING=256*1024, MAX_BATCH=64*1024;
function encode(bytes){let text='';for(const value of bytes)text+=String.fromCharCode(value);return btoa(text)}

// WebSocket-compatible channel for noVNC, transported entirely through app-only
// MCP tools. No browser sockets, model-context updates, or automatic input retry.
export class DesktopChannel {
  constructor(call,id) {
    this.call=call;this.id=id;this.readyState=0;this.binaryType='arraybuffer';this.protocol='binary';
    this.onopen=null;this.onmessage=null;this.onclose=null;this.onerror=null;
    this.bufferedAmount=0;this.queue=[];this.sequence=0;this.cursor=0;this.timer=null;this.pumping=null;
  }
  start(){if(this.readyState!==0)return;this.readyState=1;this.onopen?.({});this.readLoop().catch(error=>this.fail(error))}
  send(value){
    if(this.readyState!==1)throw Error('Desktop disconnected; input was not sent.');
    const bytes=value instanceof ArrayBuffer?new Uint8Array(value):new Uint8Array(value.buffer,value.byteOffset,value.byteLength);
    if(this.bufferedAmount+bytes.length>MAX_PENDING){this.fail(Error('Desktop input queue is full; nothing was replayed.'));return}
    for(let offset=0;offset<bytes.length;offset+=MAX_BATCH)this.queue.push(bytes.slice(offset,offset+MAX_BATCH));
    this.bufferedAmount+=bytes.length;
    if(!this.timer&&!this.pumping)this.timer=setTimeout(()=>{this.timer=null;this.flush().catch(error=>this.fail(error))},30);
  }
  async flush(){
    clearTimeout(this.timer);this.timer=null;
    if(this.pumping)return this.pumping;
    this.pumping=this.pump();
    try{await this.pumping}finally{this.pumping=null;if(this.queue.length&&this.readyState===1)this.timer=setTimeout(()=>{this.timer=null;this.flush().catch(error=>this.fail(error))},0)}
  }
  async pump(){
    while(this.queue.length&&this.readyState===1){
      const parts=[];let size=0;
      while(this.queue.length&&size+this.queue[0].length<=MAX_BATCH){const part=this.queue.shift();parts.push(part);size+=part.length}
      const bytes=new Uint8Array(size);let offset=0;for(const part of parts){bytes.set(part,offset);offset+=part.length}
      try{await this.call('desktop_action',{desktopId:this.id,action:'write',sequence:++this.sequence,data:encode(bytes)})}
      catch{this.fail(Error('Desktop input delivery was not confirmed. Reopen the desktop; input was not retried.'));return}
      if(this.readyState!==1)return;
      this.bufferedAmount-=size;
    }
  }
  async readLoop(){
    while(this.readyState===1){
      const data=await this.call('desktop_read',{desktopId:this.id,after:this.cursor});
      if(this.readyState!==1)return;
      if(!Array.isArray(data.chunks)||data.chunks.length>16||!Number.isSafeInteger(data.cursor)||data.cursor<this.cursor)throw Error('Invalid desktop stream response');
      let total=0;
      for(const chunk of data.chunks){
        if(typeof chunk!=='string'||chunk.length>174764)throw Error('Desktop stream chunk exceeds its bound');
        const bytes=Uint8Array.from(atob(chunk),character=>character.charCodeAt(0));total+=bytes.length;
        if(total>128*1024)throw Error('Desktop stream response exceeds its bound');
        this.onmessage?.({data:bytes.buffer});
      }
      this.cursor=data.cursor;
      if(data.closed){this.fail(Error(data.reason||'The desktop upstream connection ended. Reopen to reconnect.'));return}
      // Yield to rendering and user events under sustained graphics traffic.
      await new Promise(resolve=>setTimeout(resolve,16));
    }
  }
  fail(error){if(this.readyState===3)return;this.lastError=error.message;this.onerror?.({message:error.message,error});this.close(1011,error.message)}
  close(code=1000,reason='Desktop disconnected'){
    if(this.readyState===3)return this.closing;
    this.readyState=3;clearTimeout(this.timer);this.queue=[];this.bufferedAmount=0;
    this.closing=this.call('desktop_action',{desktopId:this.id,action:'close'}).catch(()=>{});
    this.onclose?.({code,reason,wasClean:code===1000});
    return this.closing;
  }
}
