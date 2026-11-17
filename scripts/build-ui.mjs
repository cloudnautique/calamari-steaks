import { mkdir, copyFile, readFile, writeFile, readdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';

async function brandTokens() {
  if (process.env.DISCOBOX_BRAND_TOKENS) return process.env.DISCOBOX_BRAND_TOKENS;
  const checkout = fileURLToPath(new URL('discobox/assets/brand/tokens.css', root));
  if (existsSync(checkout)) return checkout;
  let moduleDir;
  try {
    execFileSync('go', ['mod', 'download', 'github.com/discobox-ai/discobox'], { cwd: fileURLToPath(root), stdio: 'ignore' });
    moduleDir = execFileSync('go', ['list', '-m', '-f', '{{.Dir}}', 'github.com/discobox-ai/discobox'], { cwd: fileURLToPath(root), encoding: 'utf8' }).trim();
  } catch (error) {
    throw new Error(`Could not locate the pinned Discobox Go module for brand tokens (is Go installed?): ${error.message}`);
  }
  const tokens = `${moduleDir}/assets/brand/tokens.css`;
  if (!existsSync(tokens)) throw new Error(`Brand tokens missing from ${moduleDir}`);
  return tokens;
}

const root = new URL('../', import.meta.url);
const destination = new URL('web/vendor/', root);
await mkdir(destination, { recursive: true });
const bundled = await build({
  entryPoints: [fileURLToPath(new URL('web/app.js', root))],
  outfile: fileURLToPath(new URL('app.js', destination)),
  bundle: true, format: 'esm', platform: 'browser', target: 'es2022',
  minify: true, loader: { '.wasm': 'binary' }, legalComments: 'inline', metafile: true,
});
await build({entryPoints:[fileURLToPath(new URL('web/onboarding.js',root))],outfile:fileURLToPath(new URL('onboarding.js',destination)),bundle:true,format:'esm',platform:'browser',target:'es2022',minify:true,legalComments:'inline'});
for (const [source, name] of [
  ['ghostty-web/dist/ghostty-web.js', 'ghostty-web.js'],
  ['ghostty-web/ghostty-vt.wasm', 'ghostty-vt.wasm'],
  ['@modelcontextprotocol/ext-apps/dist/src/app-with-deps.js', 'mcp-app.js'],
]) {
  await copyFile(new URL(`node_modules/${source}`, root), new URL(name, destination));
}
// Brand values stay owned by Discobox, never duplicated in app CSS. They come
// from the Go module this adapter pins, so no separate checkout is needed;
// DISCOBOX_BRAND_TOKENS or a ./discobox checkout overrides it.
// Copy the bytes, not the file: the Go module cache is read-only, and a
// copied mode would block the next build from overwriting it.
await writeFile(new URL('tokens.css', destination), await readFile(await brandTokens()));
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
