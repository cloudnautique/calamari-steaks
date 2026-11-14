import {spawnSync} from 'node:child_process';
import {mkdir, readFile, writeFile, copyFile, chmod, rm} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';

const root=fileURLToPath(new URL('../',import.meta.url));
const targets=process.argv.slice(2);
if(!targets.length)targets.push('darwin-arm64');
const supported=new Set(['darwin-arm64','darwin-amd64','linux-amd64','linux-arm64']);
for(const target of targets)if(!supported.has(target))throw Error(`Unsupported target: ${target}. Choose ${[...supported].join(', ')}.`);
function run(command,args,env={}){
 const result=spawnSync(command,args,{cwd:root,env:{...process.env,...env},stdio:'inherit'});
 if(result.error)throw result.error;
 if(result.status!==0)throw Error(`${command} failed (${result.status})`);
}
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
 manifest.compatibility.platforms=[os];
 await writeFile(new URL('manifest.json',stage),JSON.stringify(manifest,null,2)+'\n');
 const binary=fileURLToPath(new URL('server/discobox-mcp',stage));
 run('go',['build','-trimpath','-ldflags=-s -w','-o',binary,'./cmd/discobox-mcp'],{GOOS:os,GOARCH:arch,CGO_ENABLED:'0'});
 await chmod(binary,0o755);
 await copyFile(new URL('../web/vendor/THIRD-PARTY-NOTICES.txt',import.meta.url),new URL('THIRD-PARTY-NOTICES.txt',stage));
 await copyFile(new URL('../docs/desktop-extension.md',import.meta.url),new URL('INSTALL.md',stage));
 const output=new URL(`discobox-mcp-${manifest.version}-${target}.mcpb`,dist);
 run(process.execPath,['node_modules/@anthropic-ai/mcpb/dist/cli/cli.js','pack',fileURLToPath(stage),fileURLToPath(output)]);
 const hash=createHash('sha256').update(await readFile(output)).digest('hex');
 await writeFile(`${fileURLToPath(output)}.sha256`,`${hash}  ${output.pathname.split('/').at(-1)}\n`);
 console.log(`Built ${fileURLToPath(output)}`);
}
