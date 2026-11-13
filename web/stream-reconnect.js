// Reconnection restores observation only. This scheduler never stores or sends input.
export class StreamReconnect {
  constructor(reconnect, schedule=(fn,delay)=>setTimeout(fn,delay), cancel=id=>clearTimeout(id)) { this.reconnect=reconnect;this.schedule=schedule;this.cancelTimer=cancel;this.timer=undefined;this.attempt=0; }
  request() {
    if(this.timer!==undefined)return;
    const delay=Math.min(10000,1000*2**Math.min(this.attempt++,4));
    this.timer=this.schedule(()=>{this.timer=undefined;this.reconnect()},delay);
  }
  cancel() { if(this.timer!==undefined)this.cancelTimer(this.timer);this.timer=undefined;this.attempt=0; }
}
export function canReconnect(error) {
 return !error?.sessionEnded && !error?.sessionChanged && !/HTTP (401|403)|is archived|adoption approval/i.test(error?.message||'');
}
