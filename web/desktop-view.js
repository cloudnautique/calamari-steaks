import RFB from '@novnc/novnc';
import {DesktopChannel} from './desktop-channel.js';
import {FocusControl} from './focus-control.js';
const $=id=>document.getElementById(id);

export class DesktopView {
  constructor(call,activity){
    this.call=call;this.activity=activity;this.epoch=0;this.hasControl=false;
    this.screen=$('desktop-screen');this.status=$('desktop-status');
    $('desktop-reconnect').onclick=()=>this.open(this.box).catch(error=>this.report(error));
    this.screen.addEventListener('focusin',()=>this.syncFocus());
    this.screen.addEventListener('focusout',()=>setTimeout(()=>this.syncFocus(),0));
    window.addEventListener('blur',()=>this.yield());
    window.addEventListener('focus',()=>this.syncFocus());
    document.addEventListener('visibilitychange',()=>document.hidden?this.yield():this.syncFocus());
  }
  report(error){this.status.textContent=error.message}
  async open(box){
    const closing=this.close(),epoch=this.epoch;this.box=box;this.active=true;
    this.status.textContent='Connecting desktop…';$('desktop-reconnect').disabled=true;
    await closing;if(epoch!==this.epoch)return;
    let opened;
    try{opened=await this.call('desktop_open',{boxId:box})}catch(error){if(epoch===this.epoch){this.report(error);$('desktop-reconnect').disabled=false}return}
    if(epoch!==this.epoch){this.call('desktop_action',{desktopId:opened.desktopId,action:'close'}).catch(()=>{});return}
    const channel=this.channel=new DesktopChannel(this.call,opened.desktopId);
    const control=this.control=new FocusControl(
      async()=>{await this.call('desktop_action',{desktopId:opened.desktopId,action:'claim'});if(this.rfb&&this.channel===channel){this.rfb.viewOnly=false;this.hasControl=true;this.status.textContent='You control the desktop · assistant writes paused';this.activity('desktop_control_changed',{boxId:box,owned:true})}},
      async()=>{this.hasControl=false;if(this.rfb&&this.channel===channel)this.rfb.viewOnly=true;await channel.flush();await this.call('desktop_action',{desktopId:opened.desktopId,action:'release'});if(this.channel===channel)this.status.textContent='Desktop live · focus it to control';this.activity('desktop_control_changed',{boxId:box,owned:false})},
    );
    try{
      const rfb=this.rfb=new RFB(this.screen,channel,{shared:true});this.rfbActive=true;
      rfb.viewOnly=true;rfb.scaleViewport=true;rfb.resizeSession=false;rfb.background='#000';
      rfb.qualityLevel=5;rfb.compressionLevel=6;
      rfb.addEventListener('connect',()=>{if(epoch!==this.epoch)return;this.status.textContent='Desktop live · focus it to control';$('desktop-reconnect').disabled=false;this.syncFocus();this.activity('desktop_connected',{boxId:box})});
      rfb.addEventListener('disconnect',()=>{if(this.rfb===rfb)this.rfbActive=false;if(epoch!==this.epoch)return;this.hasControl=false;this.status.textContent=channel.lastError||'Desktop disconnected · reopen to reconnect; input is never replayed';$('desktop-reconnect').disabled=false;channel.close()});
      rfb.addEventListener('credentialsrequired',()=>{if(epoch!==this.epoch)return;this.close();this.status.textContent='Desktop requires credentials. Configure them in Discobox; do not paste them into chat.'});
      rfb.addEventListener('securityfailure',()=>{if(epoch===this.epoch)this.status.textContent='Desktop authentication failed. Check its configuration in Discobox.'});
      channel.onerror=event=>{if(epoch===this.epoch)this.report(event.error)};
      channel.start();
    }catch(error){control.focus(false).catch(()=>{});channel.close();this.report(error);$('desktop-reconnect').disabled=false}
  }
  syncFocus(){
    if(!this.control||!this.active)return;
    const focused=document.hasFocus()&&!document.hidden&&this.screen.contains(document.activeElement);
    const epoch=this.epoch;this.control.focus(focused).catch(error=>{if(epoch===this.epoch)this.report(error)});
  }
  async yield(){if(this.rfb)this.rfb.viewOnly=true;await this.control?.focus(false).catch(error=>this.report(error))}
  close(){
    this.epoch++;this.active=false;this.hasControl=false;
    if(this.rfb){this.rfb.viewOnly=true;if(this.rfbActive)this.rfb.disconnect();this.rfb=null;this.rfbActive=false}
    const closing=this.channel?.close();this.channel=null;this.control=null;
    this.screen.replaceChildren();$('desktop-reconnect').disabled=false;
    return closing;
  }
}
