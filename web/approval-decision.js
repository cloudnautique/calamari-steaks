export function approvalPlan(review,selection){
 const request=review?.request;
 if(!request||request.status!=='pending'||!review.reviewToken)throw Error('This request is settled or its review must be refreshed.');
 const result={reviewToken:review.reviewToken};let approves=false;
 for(const part of ['use','delegate']){
  if(!request[part])continue;
  const decision=selection[part];
  if(!['approve','deny'].includes(decision))throw Error('Choose a decision for each requested part.');
  result[part]=decision;approves ||= decision==='approve';
 }
 if(!request.use&&!request.delegate)throw Error('This request has no reviewable parts.');
 if(approves){
  const credential=review.credentials?.find(choice=>choice.id===selection.secretId);
  if(!credential)throw Error('Choose an available credential.');
  const ttl=Number(selection.ttlSeconds);
  if(!Number.isInteger(ttl)||ttl<1||ttl>2592000)throw Error('Choose a duration between 1 second and 30 days.');
  if(credential.maxBindingTTLSeconds>0&&ttl>credential.maxBindingTTLSeconds)throw Error('The duration exceeds this credential’s limit.');
  result.secretId=credential.id;result.ttlSeconds=ttl;
 }
 return result;
}
function scopeLabel(part){return part.routes?.length?'requested routes only':'no additional route restriction; credential and delegation limits apply'}
export function decisionSummary(request,plan,credentials){
 const lines=[`Request ${request.id}`,`Box ${request.sandboxId}`];
 if(request.use)lines.push(`Use in this box: ${plan.use}${plan.use==='approve'?` · ${request.use.add?'add to existing access':'replace existing use grant'} · ${scopeLabel(request.use)}`:''}`);
 if(request.delegate)lines.push(`Pass to workers: ${plan.delegate}${plan.delegate==='approve'?' · '+scopeLabel(request.delegate):''}`);
 if(plan.secretId)lines.push(`Credential: ${credentials.find(choice=>choice.id===plan.secretId)?.name||plan.secretId} (${plan.secretId})`,`Duration: ${plan.ttlSeconds} seconds`);
 return lines.join('\n');
}
