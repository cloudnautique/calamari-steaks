import {App} from '@modelcontextprotocol/ext-apps';
import {widgetHeight} from './terminal-layout.js';
import {renderSteps} from './onboarding-steps.js';
import {sendToChat} from './chat-share.js';
const byId=id=>document.getElementById(id);
let app,token,state,busy=false;
const embedded=window.parent!==window;
function report(error){byId('error').textContent=error.message;byId('error').hidden=false;}
function selectedMode(){return document.querySelector('input[name=mode]:checked').value;}
function modeChanged(){const remote=selectedMode()==='remote';byId('authorization').hidden=!remote;byId('relays-panel').hidden=!remote;byId('endpoint-label').textContent=remote?'Server address':'Local connection';byId('endpoint-help').textContent=remote?'Enter the server’s discobox://d1-… address. This is different from this adapter’s peer ID.':'Use the default local endpoint, a custom unix:///absolute/path, or a Windows npipe:////./pipe/name.';}
function config(){return{endpoint:byId('endpoint').value.trim(),project:byId('project').value.trim(),relays:byId('relays').value.split('\n').map(s=>s.trim()).filter(Boolean)};}
async function call(name,args={}){
 if(app){const r=await app.callServerTool({name,arguments:args});if(r.isError)throw Error(r.content?.find(c=>c.type==='text')?.text||'The action could not complete.');return r._meta?.onboarding||r.structuredContent;}
 const action={connection_status:'status',get_connection_peer_id:'identity',connection_test:'test',connection_save:'save'}[name];
 const r=await fetch(`/api/connection/${action}`,{method:'POST',headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json'},body:JSON.stringify(args)});if(!r.ok)throw Error(await r.text());return r.json();
}
function render(value){
 state=value;const c=value.config;
 document.querySelector(`input[name=mode][value=${c.endpoint.startsWith('discobox://')?'remote':'local'}]`).checked=true;
 byId('endpoint').value=c.endpoint;byId('project').value=c.project;byId('relays').value=(c.relays||[]).join('\n');modeChanged();
 const managedAPI=!c.endpoint.startsWith('unix://')&&!c.endpoint.startsWith('npipe://')&&!c.endpoint.startsWith('discobox://');byId('mode-choices').hidden=managedAPI;if(managedAPI){byId('endpoint-label').textContent='Managed API connection';byId('endpoint-help').textContent='This API endpoint is supplied by the launcher.';}
 byId('settings').disabled=value.managed;byId('managed').hidden=!value.managed;byId('save').hidden=value.managed;
 byId('status').textContent=value.message;byId('resolved').textContent=value.resolvedEndpoint?`Resolved address: ${value.resolvedEndpoint}`:'';
 byId('restart').hidden=!value.restartRequired||(value.canApply&&value.state!=='restart_required');byId('next').hidden=value.state!=='connected'||value.restartRequired;
 renderSteps(byId('diagnosis'),value.steps);
 if(value.peerId){byId('peer-id').value=value.peerId;byId('enrollment-command').value=value.enrollmentCommand;byId('enrollment').hidden=false;}
}
function resize(){const height=widgetHeight(app?.getHostContext(),false,window.innerHeight);document.documentElement.style.setProperty('--setup-height',embedded?`${height}px`:'100dvh');if(app)app.sendSizeChanged({height}).catch(report);}
async function run(action){if(busy)return;busy=true;byId('error').hidden=true;byId('test').disabled=byId('save').disabled=byId('identity').disabled=true;byId('settings').disabled=true;try{await action();}catch(e){report(e);}finally{busy=false;byId('test').disabled=byId('save').disabled=byId('identity').disabled=false;byId('settings').disabled=!!state?.managed;resize();}}
for(const radio of document.querySelectorAll('input[name=mode]'))radio.onchange=()=>{byId('endpoint').value=selectedMode()==='remote'?'':(state?.localDefault||'unix://');byId('relays').value='';modeChanged();byId('status').textContent='Settings changed. Test or save this connection.';byId('next').hidden=true;};
for(const input of [byId('endpoint'),byId('project'),byId('relays')])input.oninput=()=>{byId('status').textContent='Settings changed. Test or save this connection.';byId('next').hidden=true;};
byId('test').onclick=()=>run(async()=>{byId('status').textContent='Testing connection…';const result=await call('connection_test',config());byId('status').textContent=result.message;renderSteps(byId('diagnosis'),result.steps);byId('next').hidden=result.state!=='connected'||state.restartRequired||byId('endpoint').value.trim()!==state.config.endpoint||byId('project').value.trim()!==state.config.project||config().relays.join('\n')!==(state.config.relays||[]).join('\n');});
byId('save').onclick=()=>run(async()=>{byId('status').textContent='Saving and connecting…';render(await call('connection_save',config()));});
// Hand off to chat instead of asking the person to retype a request.
byId('show-boxes').onclick=()=>run(async()=>{
 if(!app){byId('status').textContent='Ask chat: “Show my Discobox boxes.”';return;}
 try{byId('status').textContent=await sendToChat(app,'Show my Discobox boxes.',[]);}
 catch(e){byId('status').textContent=e.copyPrompt?'Ask chat: “Show my Discobox boxes.”':e.message;}
});
byId('identity').onclick=()=>run(async()=>{const value=await call('get_connection_peer_id');byId('peer-id').value=value.peerId;byId('enrollment-command').value=value.enrollmentCommand;byId('enrollment').hidden=false;});
byId('copy').onclick=async()=>{const field=byId('enrollment-command');try{await navigator.clipboard.writeText(field.value);byId('status').textContent='Enrollment command copied. Run it on the intended server computer.';}catch{field.focus();field.select();byId('status').textContent='Copy the selected command manually, then run it on the server computer.';}};
window.addEventListener('resize',resize);
try{
 if(embedded){app=new App({name:'Discobox connection setup',version:'0.12.0'},{},{autoResize:false});app.ontoolresult=result=>{const value=result._meta?.onboarding||result.structuredContent;if(value?.config&&!busy)render(value);};app.onhostcontextchanged=resize;await app.connect(undefined,{timeout:15000});}
 else{token=new URLSearchParams(location.hash.slice(1)).get('token');history.replaceState(null,'',location.pathname);if(!token)throw Error('Use the private setup link printed by your MCP adapter.');}
 render(await call('connection_status'));resize();
}catch(e){report(e);byId('status').textContent='Setup could not initialize.';}
