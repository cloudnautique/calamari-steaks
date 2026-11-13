import {marked} from 'marked';
import DOMPurify from 'dompurify';
import {FilesModel,parentDirectory,resolveFileLink} from './files-model.js';
const $=id=>document.getElementById(id);
const allowedTags=['h1','h2','h3','h4','h5','h6','p','a','strong','em','del','ul','ol','li','pre','code','blockquote','table','thead','tbody','tr','th','td','hr','br','details','summary'];
export class FilesView {
  constructor({call,activity,external,share,download}) {
    this.activity=activity;this.external=external;this.share=share;this.download=download;this.active=false;this.raw=false;this.signature='';
    this.model=new FilesModel(call,state=>this.render(state));
    $('files-go').onclick=() => this.model.list($('files-path').value);
    $('files-path').onkeydown=event=>{if(event.key==='Enter'){event.preventDefault();this.model.list($('files-path').value)}};
    $('files-up').onclick=()=>this.model.list(parentDirectory(this.model.state.directory));
    $('files-home').onclick=()=>this.model.list('/workspace');
    $('files-more').onclick=()=>this.model.moreEntries();
    $('file-more').onclick=()=>this.model.more();
    $('file-raw').onclick=()=>{this.raw=!this.raw;this.signature='';this.render(this.model.state)};
    $('file-share').onclick=()=>this.perform(()=>this.share(this.model.state));
    $('file-download').onclick=()=>this.perform(()=>this.download(this.model.state.file));
    $('file-copy').onclick=()=>this.perform(async()=>{try{await navigator.clipboard.writeText(this.model.state.file.uri);$('file-notice').textContent='Resource URI copied.'}catch{$('file-uri').hidden=false;$('file-uri').focus();$('file-uri').select();$('file-notice').textContent='Copy the selected resource URI.'}});
    $('file-preview').onclick=event=>{
      const link=event.target.closest('a');if(!link)return;event.preventDefault();
      try {const target=resolveFileLink(link.getAttribute('href'),this.model.state.file.uri);if(!target)throw Error('This link is not a file in the selected box or an HTTP(S) URL.');if(target.external)this.perform(()=>this.external(target.external));else if(target.path===this.model.state.file.path&&target.hash){}else this.openFile(target.path);}catch(error){$('file-notice').textContent=error.message}
    };
  }
  async perform(action) {try{await action()}catch(error){$('file-notice').textContent=error.message}}
  async open(boxId,path) {
    this.model.bind(boxId);this.setActive(true);
    if(path)await Promise.all([this.model.list(parentDirectory(path)),this.openFile(path)]);
    else if(!this.model.state.entries.length)await this.model.list();
  }
  async openFile(path){const boxId=this.model.state.boxId;await this.model.open(path);const {file,error}=this.model.state;if(this.active&&boxId===this.model.state.boxId&&file?.path===path&&!error&&file?.uri)this.activity('file_opened',{boxId:this.model.state.boxId,path:file.path,uri:file.uri,version:file.version})}
  setActive(value){this.active=value;clearTimeout(this.timer);if(value)this.poll()}
  poll(){this.timer=setTimeout(async()=>{if(!this.active)return;if(!document.hidden)await this.model.checkVersion();if(this.active)this.poll()},5000)}
  close(){this.active=false;clearTimeout(this.timer);this.model.close()}
  render(state){
    $('files-path').value=state.directory;$('files-up').disabled=state.directory==='/';$('files-status').textContent=state.listing?'Loading folder…':`${state.entries.length} items · box ${state.boxId||''}`;
    const list=$('files-list');const listScroll=list.scrollTop;list.replaceChildren();$('files-more').hidden=!state.hasMoreEntries;$('files-more').disabled=state.listing;
    if(!state.entries.length&&!state.listing){const empty=document.createElement('p');empty.className='muted';empty.textContent=state.error?'Folder unavailable. Enter a path to try another folder.':'This folder is empty.';list.append(empty)}
    for(const entry of state.entries){const row=document.createElement('button');row.className='file-row';row.classList.toggle('selected',entry.path===state.file?.path);row.disabled=!['file','directory'].includes(entry.type);const name=document.createElement('span');name.textContent=(entry.type==='directory'?'▸ ':'')+entry.name;const size=document.createElement('small');size.textContent=entry.type==='directory'?'Folder':`${entry.size.toLocaleString()} B`;row.append(name,size);row.title=entry.path;row.onclick=()=>entry.type==='directory'?this.model.list(entry.path):this.openFile(entry.path);list.append(row)}
    list.scrollTop=listScroll;
    const file=state.file;
    $('file-title').textContent=file?.name||'Choose a file';$('file-location').textContent=file?.path||'Browse a folder, then open a report or source file.';
    $('file-notice').textContent=state.error||'';$('file-uri').value=file?.uri||'';
    $('file-metadata').textContent=state.loading?'Reading file…':file?.version?`${file.size.toLocaleString()} bytes · ${file.mimeType==='text/markdown'?'Markdown':file.mimeType} · ${state.truncated?`showing ${state.nextOffset.toLocaleString()} bytes · `:''}${file.modifiedAt?'Updated '+new Date(file.modifiedAt).toLocaleString():'Current file'}`:'';
    for(const id of ['file-copy','file-share','file-download','file-raw'])$(id).disabled=!file?.version||state.loading;
    $('file-share').disabled ||= !!state.error || !state.text;
    $('file-raw').disabled ||= !file || !/\.(md|markdown)$/i.test(file.path);
    $('file-more').hidden=!state.truncated;$('file-more').disabled=state.loading;
    $('file-raw').textContent=this.raw?'Rendered':'Source';
    const signature=`${file?.uri}:${file?.version}:${state.nextOffset}:${this.raw}:${state.loading}`;
    if(signature!==this.signature){this.signature=signature;const preview=$('file-preview'),scroll=preview.scrollTop;
      preview.replaceChildren();
      if(!file){const message=document.createElement('p');message.className='muted';message.textContent='Open a file to see its actual contents here.';preview.append(message)}
      else if(state.loading&&!state.text){preview.textContent='Reading file…'}
      else if(/\.(md|markdown)$/i.test(file.path)&&!this.raw){preview.innerHTML=DOMPurify.sanitize(marked.parse(state.text),{ALLOWED_TAGS:allowedTags,ALLOWED_ATTR:['href','title'],ALLOW_DATA_ATTR:false,SANITIZE_NAMED_PROPS:true,ALLOWED_URI_REGEXP:/^(?:(?:https?|discobox-file):|[^a-z]|[a-z+.-]+(?:[^a-z+.-:]|$))/i});}
      else {const source=document.createElement('pre');source.textContent=state.text;preview.append(source)}
      preview.scrollTop=scroll;
    }
  }
}
