// Opens a scaffolded crew the way VS Code does and reports every Problem it would show.
//
// It runs Red Hat's yaml-language-server, the engine of the Red Hat YAML extension, over
// the crew's files with the settings `kmctl create` wrote to .vscode/settings.json: the
// schema mapping (yaml.schemas) and the Helm association (files.associations), which
// keeps a chart's templates out of the YAML checks as VS Code does. Nothing leaves this
// machine: the schema comes from a local directory over 127.0.0.1 and the schema store
// is off.
//
//   node check.mjs CREW_DIR SCHEMA_DIR [--expect-clean | --expect-errors]
//
// Prints one line per Problem and a summary. --expect-clean (the default) exits 1 on any
// Problem; --expect-errors exits 1 when there is none, which proves a check is not vacuous.
// --no-helm-association treats a chart's templates as plain YAML, as they are without
// the association.
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const args = process.argv.slice(2);
const flags = args.filter((a) => a.startsWith('--'));
const [crewDir, schemaDir] = args.filter((a) => !a.startsWith('--'));
if (!crewDir || !schemaDir) {
  console.error('usage: node check.mjs CREW_DIR SCHEMA_DIR [--expect-clean | --expect-errors] [--no-helm-association]');
  process.exit(2);
}
const expectErrors = flags.includes('--expect-errors');
const useHelmAssociation = !flags.includes('--no-helm-association');

const walk = (dir) =>
  fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(path.join(dir, e.name)) : [path.join(dir, e.name)]));

// The files VS Code gives to the YAML language: *.yaml that no files.associations entry claims.
// path.matchesGlob is Node's experimental glob match; the one association kmctl writes is plain.
function yamlFiles(settings) {
  const associations = useHelmAssociation ? Object.keys(settings['files.associations'] ?? {}) : [];
  return walk(crewDir)
    .filter((f) => f.endsWith('.yaml'))
    .filter((f) => !associations.some((glob) => path.matchesGlob(path.relative(crewDir, f), glob)));
}

const server = http.createServer((req, res) => {
  const file = path.join(schemaDir, path.basename(req.url ?? ''));
  if (!fs.existsSync(file)) {
    res.writeHead(404).end();
    return;
  }
  res.writeHead(200, { 'content-type': 'application/json' }).end(fs.readFileSync(file));
});
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const origin = `http://127.0.0.1:${server.address().port}`;

const settings = JSON.parse(fs.readFileSync(path.join(crewDir, '.vscode', 'settings.json'), 'utf8'));
// The settings name the schema by its published URL; the check serves the same file locally.
const schemas = Object.fromEntries(
  Object.entries(settings['yaml.schemas']).map(([url, globs]) => [`${origin}/${path.basename(new URL(url).pathname)}`, globs]),
);

// What VS Code answers when the server asks for these sections of the user's settings.
const editorSettings = { yaml: { validate: true, schemas, schemaStore: { enable: false }, completion: true, hover: true }, http: {}, '[yaml]': {}, editor: {} };

const ls = spawn(process.execPath, [path.join(import.meta.dirname, 'node_modules', 'yaml-language-server', 'bin', 'yaml-language-server'), '--stdio'], {
  stdio: ['pipe', 'pipe', 'inherit'],
});
let buffer = Buffer.alloc(0);
const pending = new Map();
const diagnostics = new Map();
let nextId = 1;

function send(message) {
  const body = Buffer.from(JSON.stringify({ jsonrpc: '2.0', ...message }));
  ls.stdin.write(Buffer.concat([Buffer.from(`Content-Length: ${body.length}\r\n\r\n`), body]));
}
const request = (method, params) =>
  new Promise((resolve, reject) => {
    const id = nextId++;
    pending.set(id, { resolve, reject });
    send({ id, method, params });
  });

function handle(message) {
  if (message.id !== undefined && (message.result !== undefined || message.error)) {
    const p = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) p?.reject(new Error(message.error.message));
    else p?.resolve(message.result);
  } else if (message.method === 'textDocument/publishDiagnostics') {
    diagnostics.set(message.params.uri, message.params.diagnostics);
  } else if (message.id !== undefined) {
    // A request from the server: it pulls its settings as workspace/configuration, one
    // answer per section asked for; anything else (registerCapability) gets an empty answer.
    const answer = message.method === 'workspace/configuration' ? message.params.items.map((i) => editorSettings[i.section] ?? null) : null;
    send({ id: message.id, result: answer });
  }
}

ls.stdout.on('data', (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  for (;;) {
    const end = buffer.indexOf('\r\n\r\n');
    if (end < 0) return;
    const length = Number(/Content-Length: (\d+)/i.exec(buffer.subarray(0, end).toString())?.[1]);
    if (buffer.length < end + 4 + length) return;
    handle(JSON.parse(buffer.subarray(end + 4, end + 4 + length).toString()));
    buffer = buffer.subarray(end + 4 + length);
  }
});

const root = pathToFileURL(path.resolve(crewDir)).href;
await request('initialize', {
  processId: process.pid,
  rootUri: root,
  capabilities: { textDocument: { publishDiagnostics: {} }, workspace: { configuration: true, didChangeConfiguration: { dynamicRegistration: false } } },
  workspaceFolders: [{ uri: root, name: 'crew' }],
});
send({ method: 'initialized', params: {} });
send({ method: 'workspace/didChangeConfiguration', params: { settings: null } });

const files = yamlFiles(settings);
for (const f of files) {
  send({
    method: 'textDocument/didOpen',
    params: { textDocument: { uri: pathToFileURL(f).href, languageId: 'yaml', version: 1, text: fs.readFileSync(f, 'utf8') } },
  });
}

// Wait on state: every opened file has published its diagnostics (possibly none).
const deadline = Date.now() + 60_000;
while (diagnostics.size < files.length && Date.now() < deadline) await new Promise((r) => setTimeout(r, 50));
// A schema loads asynchronously and republishes; settle until a quiet moment.
let last = -1;
for (let quiet = 0; quiet < 6 && Date.now() < deadline; quiet++) {
  await new Promise((r) => setTimeout(r, 250));
  const total = [...diagnostics.values()].reduce((n, d) => n + d.length, 0);
  quiet = total === last ? quiet : 0;
  last = total;
}

const severity = ['', 'error', 'warning', 'info', 'hint'];
let problems = 0;
for (const f of files) {
  const found = diagnostics.get(pathToFileURL(f).href);
  if (found === undefined) console.log(`NO RESULT ${path.relative(crewDir, f)}`);
  for (const d of found ?? []) {
    problems++;
    console.log(`${severity[d.severity ?? 1]} ${path.relative(crewDir, f)}:${d.range.start.line + 1}: ${d.message.split('\n')[0]}`);
  }
}
const missing = files.filter((f) => !diagnostics.has(pathToFileURL(f).href)).length;
console.log(`${files.length} YAML files checked against ${Object.keys(schemas).length} schema, ${problems} Problems`);

send({ id: nextId++, method: 'shutdown' });
ls.kill();
server.close();
const ok = expectErrors ? problems > 0 : problems === 0 && missing === 0;
process.exit(ok ? 0 : 1);
