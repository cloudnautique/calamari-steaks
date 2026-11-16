export const MAX_UPLOAD_SIZE=16*1024*1024;
export async function uploadFile(call,file,{boxId,directory,current=()=>true,progress=()=>{}}){
 if(!boxId||!directory.startsWith('/')||!file.name||/[\\/\x00]/.test(file.name)||['.','..'].includes(file.name))throw Error('Choose a file and a destination folder in this box.');
 if(file.size>MAX_UPLOAD_SIZE)throw Error('Files must be at most 16 MiB.');
 const path=`${directory.replace(/\/$/,'')}/${file.name}`;
 let uploadId;
 try{
  if(!current())throw Error('Destination changed; no file was uploaded.');
  const started=await call('file_upload',{action:'begin',boxId,path,size:file.size});
  uploadId=started.uploadId;
  if(typeof uploadId!=='string'||!Number.isSafeInteger(started.chunkSize)||started.chunkSize<1||started.chunkSize>192*1024)throw Error('Invalid upload session.');
  for(let offset=0;offset<file.size;){
   if(!current())throw Error('Destination changed; upload cancelled.');
   const bytes=new Uint8Array(await file.slice(offset,offset+started.chunkSize).arrayBuffer());
   let binary='';for(const b of bytes)binary+=String.fromCharCode(b);
   const result=await call('file_upload',{action:'chunk',uploadId,offset,data:btoa(binary)});
   if(result.offset!==offset+bytes.length)throw Error('Upload offset was not confirmed; no chunk was retried.');
   offset=result.offset;progress(offset,file.size);
  }
  if(!current())throw Error('Destination changed; upload cancelled.');
  const result=await call('file_upload',{action:'finish',uploadId});
  uploadId=undefined;
  return result.file;
 }finally{if(uploadId)await call('file_upload',{action:'cancel',uploadId}).catch(()=>{});}
}
