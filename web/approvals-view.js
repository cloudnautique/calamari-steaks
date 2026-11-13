import {approvalPlan,decisionSummary} from './approval-decision.js';
const $=id=>document.getElementById(id);
function element(tag,text,className){const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(className)node.className=className;return node}

export class ApprovalsView {
 constructor(call,activity,boxName){
  this.call=call;this.activity=activity;this.boxName=boxName;this.epoch=0;this.listEpoch=0;
  $('approval-back').onclick=()=>this.close();
  $('approval-filter').onchange=()=>this.list();
  $('approval-find').onclick=()=>this.review($('approval-request-id').value.trim());
  $('approval-request-id').onkeydown=event=>{if(event.key==='Enter'){event.preventDefault();this.review(event.target.value.trim())}};
 }
 close(){this.epoch++;this.listEpoch++;this.active=false;$('approval-pane').hidden=true;$('inbox-items').hidden=false;$('inbox-title').textContent='Needs you'}
 async open(id,status='pending'){
  this.active=true;$('inbox').hidden=false;$('approval-pane').hidden=false;$('inbox-items').hidden=true;$('inbox-title').textContent='Credential requests';
  $('approval-filter').value=['pending','approved','denied','all'].includes(status)?status:'pending';
  this.list();
  if(id)await this.review(id);else{$('approval-detail').replaceChildren(element('p','Choose a request, or look one up by its ID.','muted'))}
 }
 async list(){
  const epoch=++this.listEpoch;$('approval-list-status').textContent='Loading requests…';
  try{
   const result=await this.call('approval_list',{status:$('approval-filter').value});
   if(!this.active||epoch!==this.listEpoch)return;
   const list=$('approval-list');list.replaceChildren();
   for(const item of result.requests){
    const button=element('button',`${item.name||'Credential'} · ${item.status}`,'approval-row');button.append(element('small',`${this.boxName(item.boxId)||item.boxId} · ${item.id}`));
    button.onclick=()=>this.review(item.id);list.append(button);
   }
   $('approval-list-status').textContent=result.requests.length?`${result.requests.length} credential requests · at most 200 shown`:'No credential requests in this status. Past requests can be found using Approved, Denied, or All.';
  }catch(error){if(this.active&&epoch===this.listEpoch)$('approval-list-status').textContent=error.message}
 }
 async refresh(){await this.list();if(this.requestId)await this.review(this.requestId)}
 async review(id){
  if(!id){$('approval-list-status').textContent='Enter a request ID.';return}
  const epoch=++this.epoch;this.requestId=id;this.data=null;this.confirmation=null;
  $('approval-request-id').value=id;$('approval-detail').replaceChildren(element('p','Loading request…'));
  try{
   const data=await this.call('approval_review',{requestId:id});
   if(!this.active||epoch!==this.epoch)return;
   this.data=data;this.render(data);
   if($('approval-filter').value!=='all'&&$('approval-filter').value!==data.request.status){$('approval-filter').value=data.request.status;this.list()}
   this.activity('request_reviewed',{requestId:id,boxId:data.request.sandboxId,status:data.request.status});
  }catch(error){if(this.active&&epoch===this.epoch)$('approval-detail').replaceChildren(element('p',error.message,'approval-error'))}
 }
 render(data){
  const request=data.request,detail=$('approval-detail');detail.replaceChildren();
  detail.append(element('h3',`${request.name||'Credential'} · ${request.status}`));
  detail.append(element('code',request.id),element('p',`Box: ${this.boxName(request.sandboxId)||request.sandboxId} · ${request.sandboxId}`));
  if(request.createdAt)detail.append(element('p','Requested '+new Date(request.createdAt).toLocaleString(),'muted'));
  if(request.envName)detail.append(element('p',`Requested environment variable: ${request.envName}`,'muted'));
  if(request.justification){detail.append(element('h4','Reason supplied by the requesting box'),element('p',request.justification,'approval-reason'))}
  if(request.decidedAt||request.decidedBy)detail.append(element('p',`Decision: ${request.decidedAt?new Date(request.decidedAt).toLocaleString():''} · ${request.decidedBy||'Actor not recorded'}`,'muted'));
  const form=element('fieldset');form.className='approval-form';form.disabled=request.status!=='pending';
  this.selects={};
  for(const key of ['use','delegate']){
   const part=request[key];if(!part)continue;
   const card=element('section',undefined,'approval-part');card.append(element('h4',key==='use'?'Use in this box':'Pass access to workers'));
   if(request.status!=='pending'){card.append(element('p',`Outcome: ${part.outcome||'Not recorded'}`));if(part.approvedAs||part.approvedScope)card.append(element('p',`Recorded grant: ${part.approvedAs||''} · ${part.approvedScope||'Scope not recorded'}`,'muted'));}
   if(request.status==='pending'&&key==='use')card.append(element('p',part.add?'Adds the requested use routes alongside existing access.':'Approval replaces this box’s existing use grant for the selected credential.','muted'));
   const scope=element('details');scope.append(element('summary',`Requested access · ${part.routes?.length||0} routes`));
   scope.append(element('pre',part.routes?.length?JSON.stringify(part.routes,null,2):'Any route allowed by the selected credential and its delegation ceilings.'));
   card.append(scope);if(!part.routes?.length)card.append(element('p','This part requests no additional route restriction. The credential and parent delegation remain its limits.','approval-error'));
   for(const policy of part.policies||[])card.append(element('p',`Policy supplied by requester: ${policy.name||policy.file}${policy.description?' · '+policy.description:''}`,'muted'));
   if(request.status==='pending'){
    const label=element('label',key==='use'?'Decision for use':'Decision for delegation');const select=element('select');select.setAttribute('aria-label',label.textContent);
    select.append(new Option('Choose…',''),new Option('Deny','deny'),new Option('Approve requested routes','approve'));
    if(!data.credentials?.length)select.options[2].disabled=true;
    label.append(select);card.append(label);this.selects[key]=select;
   }
   form.append(card);
  }
  detail.append(form);
  if(request.status!=='pending'){
   detail.append(element('p','This request is settled and read-only. This view does not resubmit it or resume work.','muted'));return;
  }
  detail.append(element('p',data.authority,'approval-authority'));
  if(data.warning)detail.append(element('p',data.warning,'approval-error'));
  if(!data.credentials?.length)detail.append(element('p','No existing credentials are available to approve through this connection. A sandbox needs delegated credentials; otherwise review in the Discobox console.'));
  const credentialLabel=element('label','Existing credential');const secret=element('select');secret.setAttribute('aria-label','Existing credential');secret.append(new Option('Choose a credential…',''));
  for(const choice of data.credentials||[])secret.append(new Option(`${choice.name} · ${choice.id}`,choice.id));credentialLabel.append(secret);form.append(credentialLabel);
  const durationLabel=element('label','Duration (seconds)');const duration=element('input');duration.type='number';duration.min='1';duration.max='2592000';duration.value=String(request.ttlSeconds>0?Math.min(request.ttlSeconds,2592000):3600);duration.setAttribute('aria-label','Duration in seconds');durationLabel.append(duration);form.append(durationLabel);
  const notice=element('p',undefined,'approval-notice');notice.setAttribute('role','status');detail.append(notice);
  const confirmPanel=element('div',undefined,'approval-confirm');confirmPanel.hidden=true;const summary=element('pre');const confirm=element('button','Confirm decision');confirmPanel.append(summary,confirm);detail.append(confirmPanel);
  const review=element('button','Review decision','primary');form.append(review);
  const clear=()=>{this.confirmation=null;confirmPanel.hidden=true;notice.textContent=''};form.onchange=clear;form.oninput=clear;
  review.onclick=()=>{
   try{
    const plan=approvalPlan(data,{secretId:secret.value,ttlSeconds:duration.value,use:this.selects.use?.value,delegate:this.selects.delegate?.value});
    this.confirmation=plan;summary.textContent=decisionSummary(request,plan,data.credentials||[]);confirmPanel.hidden=false;notice.textContent='Check the box, scope, credential, duration, and both decisions before confirming.';
   }catch(error){notice.textContent=error.message}
  };
  confirm.onclick=async()=>{
   if(!this.confirmation||this.submitting)return;
   this.submitting=true;const plan=this.confirmation;this.confirmation=null;const epoch=this.epoch;
   data.reviewToken='';confirm.disabled=true;form.disabled=true;notice.textContent='Submitting decision…';
   try{
    const result=await this.call('approval_decide',plan);
    this.activity('request_decided',{requestId:request.id,boxId:request.sandboxId,status:result.summary.status,use:result.summary.use,delegate:result.summary.delegate});
    if(this.active&&epoch===this.epoch){await this.review(request.id);this.list()}
   }catch(error){if(this.active&&epoch===this.epoch){confirmPanel.hidden=true;notice.textContent=error.message+' Refresh this request to inspect its current status. No retry was sent.'}}
   finally{this.submitting=false}
  };
 }
}
