// Keep the first mouse gesture while the focus claim is in flight. noVNC
// otherwise drops it while viewOnly is still true. Never replay on failure,
// blur, tab change, disconnect, or a different desktop.
export class DesktopInputGate {
 constructor({ready,claim,current,deliver,report}){Object.assign(this,{ready,claim,current,deliver,report});this.events=[];this.pending=false;this.epoch=0;}
 capture(event){
  if(this.ready())return false;
  if(!this.pending&&event.type!=='mousedown')return false;
  if(this.events.length>=32){this.cancel();this.report(Error('Desktop input is busy; click again once control is ready.'));return true;}
  this.events.push(event);
  if(!this.pending){
   this.pending=true;const epoch=this.epoch;
   Promise.resolve().then(()=>{if(epoch===this.epoch)return this.claim();}).then(()=>{
    if(epoch!==this.epoch)return;
    const events=this.events;this.events=[];this.pending=false;
    if(this.current()&&this.ready())for(const item of events)this.deliver(item);
   }).catch(error=>{if(epoch===this.epoch){this.cancel();this.report(error);}});
  }
  return true;
 }
 cancel(){this.epoch++;this.events=[];this.pending=false;}
}
