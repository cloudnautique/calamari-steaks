import { mkdir, copyFile, readFile, writeFile, readdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const root = new URL('../', import.meta.url);
const destination = new URL('web/vendor/', root);
await mkdir(destination, { recursive: true });
const bundled = await build({
  entryPoints: [fileURLToPath(new URL('web/app.js', root))],
  outfile: fileURLToPath(new URL('app.js', destination)),
  bundle: true, format: 'esm', platform: 'browser', target: 'es2022',
  minify: true, loader: { '.wasm': 'binary' }, legalComments: 'inline', metafile: true,
});
for (const [source, name] of [
  ['ghostty-web/dist/ghostty-web.js', 'ghostty-web.js'],
  ['ghostty-web/ghostty-vt.wasm', 'ghostty-vt.wasm'],
  ['@modelcontextprotocol/ext-apps/dist/src/app-with-deps.js', 'mcp-app.js'],
]) {
  await copyFile(new URL(`node_modules/${source}`, root), new URL(name, destination));
}
// Brand values stay owned by the reference checkout, never duplicated in app CSS.
await copyFile(new URL('discobox/assets/brand/tokens.css', root), new URL('tokens.css', destination));
const notices = [];
const dependencies = new Set(['ghostty-web', '@modelcontextprotocol/ext-apps']);
for (const path of Object.keys(bundled.metafile.inputs)) {
  const match = path.match(/node_modules\/((?:@[^/]+\/)?[^/]+)/);
  if (match) dependencies.add(match[1]);
}
for (const dependency of [...dependencies].sort()) {
  const pkg = JSON.parse(await readFile(new URL(`node_modules/${dependency}/package.json`, root)));
  const directory = new URL(`node_modules/${dependency}/`, root);
  const licenseFiles = (await readdir(directory)).filter((name) => /^licen[cs]e/i.test(name)).sort();
  if (!licenseFiles.length) throw new Error(`Missing license notice for bundled dependency ${dependency}`);
  let license = (await Promise.all(licenseFiles.map((name) => readFile(new URL(name, directory), 'utf8')))).join('\n\n');
  if(dependency==='@novnc/novnc')license+='\n\nBundled pako license:\n'+await readFile(new URL('vendor/pako/LICENSE',directory),'utf8');
  notices.push(`${dependency} ${pkg.version}\n\n${license}`);
  console.log(`${dependency}: ${pkg.version}`);
}
await writeFile(new URL('THIRD-PARTY-NOTICES.txt', destination), notices.join('\n\n'));
console.log(`UI dependencies prepared in ${fileURLToPath(destination)}`);
