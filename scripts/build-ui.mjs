import { mkdir, copyFile, readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const root = new URL('../', import.meta.url);
const destination = new URL('web/vendor/', root);
await mkdir(destination, { recursive: true });
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
for (const dependency of ['ghostty-web', '@modelcontextprotocol/ext-apps']) {
  const pkg = JSON.parse(await readFile(new URL(`node_modules/${dependency}/package.json`, root)));
  const license = await readFile(new URL(`node_modules/${dependency}/LICENSE`, root), 'utf8');
  notices.push(`${dependency} ${pkg.version}\n\n${license}`);
  console.log(`${dependency}: ${pkg.version}`);
}
await writeFile(new URL('THIRD-PARTY-NOTICES.txt', destination), notices.join('\n\n'));
console.log(`UI dependencies prepared in ${fileURLToPath(destination)}`);
