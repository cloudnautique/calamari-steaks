export const FILE_CHUNK = 262144;
export function parentDirectory(path) { const parts=path.split('/').filter(Boolean);parts.pop();return '/'+parts.join('/'); }
export function resolveFileLink(href,uri) {
  if(typeof href!=='string'||!href.trim())return null;
  const base=new URL(uri);
  const local=href.startsWith('/')&&!href.startsWith('//')?'/'+base.pathname.split('/')[1]+href:href;
  const target=new URL(local,base);
  if(['https:','http:'].includes(target.protocol))return {external:target.href};
  if(target.protocol!=='discobox-file:'||target.host!==base.host||target.username||target.password||target.search)return null;
  const segments=target.pathname.split('/');
  if(segments[1]!==base.pathname.split('/')[1])return null;
  return {path:decodeURIComponent('/'+segments.slice(2).join('/')),hash:target.hash};
}
function textChunk(result,decoder) {
  if(typeof result.text==='string')return decoder.decode(new TextEncoder().encode(result.text),{stream:result.truncated});
  if(!result.blob)return '';
  return decoder.decode(Uint8Array.from(atob(result.blob),c=>c.charCodeAt(0)),{stream:result.truncated});
}
export class FilesModel {
  constructor(call,changed=()=>{}) { this.call=call;this.changed=changed;this.epoch=0;this.navigation=0;this.reading=0;this.state={boxId:null,directory:'/workspace',entries:[],file:null,text:'',loading:false,listing:false,error:'',truncated:false,nextOffset:0}; }
  emit() { this.changed(this.state); }
  bind(boxId) {
    if(this.state.boxId===boxId)return;
    this.epoch++;this.navigation++;this.reading++;
    this.state={boxId,directory:'/workspace',entries:[],file:null,text:'',loading:false,listing:false,error:'',truncated:false,nextOffset:0};this.emit();
  }
  async list(path=this.state.directory) {
    const epoch=this.epoch,request=++this.navigation,boxId=this.state.boxId;
    if(!boxId)return;
    this.state.listing=true;this.state.error='';this.emit();
    try { const data=await this.call('list_box_files',{boxId,path});if(epoch!==this.epoch||request!==this.navigation)return;this.state.directory=data.path;this.state.entries=data.entries;this.state.directoryOffset=data.nextOffset;this.state.hasMoreEntries=!!data.hasMore;this.state.totalEntries=data.total||data.entries.length; }
    catch(error){if(epoch===this.epoch&&request===this.navigation)this.state.error=error.message;}
    finally{if(epoch===this.epoch&&request===this.navigation){this.state.listing=false;this.emit();}}
  }
  async moreEntries() {
    if(this.state.listing||!this.state.hasMoreEntries)return;
    const epoch=this.epoch,request=++this.navigation,boxId=this.state.boxId,path=this.state.directory;
    this.state.listing=true;this.emit();
    try{const data=await this.call('list_box_files',{boxId,path,offset:this.state.directoryOffset,limit:100});if(epoch!==this.epoch||request!==this.navigation)return;this.state.entries.push(...data.entries);this.state.directoryOffset=data.nextOffset;this.state.hasMoreEntries=!!data.hasMore;this.state.totalEntries=data.total;}
    catch(error){if(epoch===this.epoch&&request===this.navigation)this.state.error=error.message;}
    finally{if(epoch===this.epoch&&request===this.navigation){this.state.listing=false;this.emit();}}
  }
  async open(path) {
    const epoch=this.epoch,request=++this.reading,boxId=this.state.boxId;
    if(!boxId)return;
    this.state.loading=true;this.state.error='';this.state.file={path,name:path.split('/').at(-1)};this.state.text='';this.state.truncated=false;this.emit();
    try {
      const data=await this.call('file_read',{boxId,path,length:FILE_CHUNK});if(epoch!==this.epoch||request!==this.reading)return;
      this.state.file=data.file;this.state.nextOffset=data.nextOffset;this.state.truncated=data.truncated;
      this.decoder=new TextDecoder('utf-8',{fatal:true});
      if(!data.file.mimeType.startsWith('text/')&&data.file.mimeType!=='application/json')throw Error('This file has no text preview. Use Download if your host supports it.');
      this.state.text=textChunk(data,this.decoder);
    }catch(error){if(epoch===this.epoch&&request===this.reading)this.state.error=error.message;}
    finally{if(epoch===this.epoch&&request===this.reading){this.state.loading=false;this.emit();}}
  }
  async more() {
    if(this.state.loading||!this.state.truncated)return;
    const epoch=this.epoch,request=++this.reading,boxId=this.state.boxId,file=this.state.file;
    const remaining=1048576-this.state.nextOffset;if(remaining<=0){this.state.error='Preview limit is 1 MiB. Read further byte ranges with read_box_file.';this.emit();return;}
    this.state.loading=true;this.emit();
    try {
      const data=await this.call('file_read',{boxId,path:file.path,offset:this.state.nextOffset,length:Math.min(FILE_CHUNK,remaining)});
      if(epoch!==this.epoch||request!==this.reading)return;
      if(data.file.version!==file.version)throw Error('The file changed. Its latest version will load automatically.');
      this.state.text+=textChunk(data,this.decoder);this.state.nextOffset=data.nextOffset;this.state.truncated=data.truncated;
    }catch(error){if(epoch===this.epoch&&request===this.reading)this.state.error=error.message;}
    finally{if(epoch===this.epoch&&request===this.reading){this.state.loading=false;this.emit();}}
  }
  async checkVersion() {
    const {boxId,file,loading}=this.state,epoch=this.epoch,request=this.reading;
    if(!file?.version||loading)return;
    try { const info=await this.call('file_stat',{boxId,path:file.path});if(epoch===this.epoch&&request===this.reading){if(info.version!==file.version)await this.open(file.path);else if(this.state.error.startsWith('File update check failed:')){this.state.error='';this.emit();}} }
    catch(error){if(epoch===this.epoch&&request===this.reading){this.state.error='File update check failed: '+error.message;this.emit();}}
  }
  close(){this.epoch++;this.navigation++;this.reading++;}
}
