import {spawnSync} from 'node:child_process';
import {mkdir, readFile, writeFile, copyFile, chmod, rm} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';

const root=fileURLToPath(new URL('../',import.meta.url));
const targets=process.argv.slice(2);
if(!targets.length)targets.push(process.platform==='darwin'?'darwin-universal':'darwin-arm64');
const supported=new Set(['darwin-universal','darwin-arm64','darwin-amd64','linux-amd64','linux-arm64','windows-amd64','windows-arm64']);
for(const target of targets)if(!supported.has(target))throw Error(`Unsupported target: ${target}. Choose ${[...supported].join(', ')}.`);
function run(command,args,env={}){
 const result=spawnSync(command,args,{cwd:root,env:{...process.env,...env},stdio:'inherit'});
 if(result.error)throw result.error;
 if(result.status!==0)throw Error(`${command} failed (${result.status})`);
}
if(targets.includes('darwin-universal') && process.platform!=='darwin')throw Error('darwin-universal requires macOS with Xcode command-line tools (lipo and codesign).');
run(process.execPath,['scripts/build-ui.mjs']);
const template=JSON.parse(await readFile(new URL('../packaging/mcpb/manifest.json',import.meta.url)));
const dist=new URL('../dist/',import.meta.url);
await mkdir(dist,{recursive:true});
for(const target of new Set(targets)){
 const [os,arch]=target.split('-');
 const stage=new URL(`mcpb-${target}/`,dist);
 // Package only explicitly copied release files, never the working directory.
 await rm(stage,{recursive:true,force:true});
 await mkdir(new URL('server/',stage),{recursive:true});
 const manifest=structuredClone(template);
 manifest.compatibility.platforms=[os==='windows'?'win32':os];
 const executable=os==='windows'?'discobox-mcp.exe':'discobox-mcp';
 manifest.server.entry_point=`server/${executable}`;
 manifest.server.mcp_config.command='${__dirname}/server/'+executable;
 await writeFile(new URL('manifest.json',stage),JSON.stringify(manifest,null,2)+'\n');
 const binary=fileURLToPath(new URL(`server/${executable}`,stage));
 if(target==='darwin-universal'){
  const slices=[];
  for(const sliceArch of ['arm64','amd64']){
   const slice=`${binary}.${sliceArch}`;
   run('go',['build','-trimpath','-ldflags=-s -w','-o',slice,'./cmd/discobox-mcp'],{GOOS:'darwin',GOARCH:sliceArch,CGO_ENABLED:'0'});
   slices.push(slice);
  }
  run('xcrun',['lipo','-create',...slices,'-output',binary]);
  run('xcrun',['lipo',binary,'-verify_arch','arm64','x86_64']);
  // Apple Silicon requires a valid signature. This is ad-hoc signing, not
  // Developer ID signing or notarization of the development bundle.
  run('codesign',['--force','--sign','-',binary]);
  run('codesign',['--verify','--strict',binary]);
  for(const slice of slices)await rm(slice);
 }else{
  run('go',['build','-trimpath','-ldflags=-s -w','-o',binary,'./cmd/discobox-mcp'],{GOOS:os,GOARCH:arch,CGO_ENABLED:'0'});
 }
 await chmod(binary,0o755);
 await copyFile(new URL('../web/vendor/THIRD-PARTY-NOTICES.txt',import.meta.url),new URL('THIRD-PARTY-NOTICES.txt',stage));
 await copyFile(new URL('../docs/desktop-extension.md',import.meta.url),new URL('INSTALL.md',stage));
 for(const name of ['connection-setup-widget.md','services.md']) await copyFile(new URL(`../docs/${name}`,import.meta.url),new URL(name,stage));
 const output=new URL(`discobox-mcp-${manifest.version}-${target}.mcpb`,dist);
 run(process.execPath,['node_modules/@anthropic-ai/mcpb/dist/cli/cli.js','pack',fileURLToPath(stage),fileURLToPath(output)]);
 const hash=createHash('sha256').update(await readFile(output)).digest('hex');
 await writeFile(`${fileURLToPath(output)}.sha256`,`${hash}  ${output.pathname.split('/').at(-1)}\n`);
 console.log(`Built ${fileURLToPath(output)}`);
}
