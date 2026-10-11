import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, mkdir, readFile, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const run = promisify(execFile);
const [version, receiptArgument] = process.argv.slice(2);
assert.equal(process.argv.length, 4, 'Usage: node scripts/linux-published-npm-acceptance.mjs EXACT_VERSION RECEIPT_JSON');
assert.match(version, /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/, 'An exact published version is required, never latest');
const nativeLinux = process.platform === 'linux' && process.arch === 'x64';
assert.ok(nativeLinux || (process.platform === 'darwin' && process.arch === 'arm64' && process.env.SESSIONS_ACCEPTANCE_FIXTURE === '1'), 'Native Linux AMD64 host required; macOS ARM64 fixture must be explicitly selected');
const receiptPath = resolve(receiptArgument);
const root = await mkdtemp('/tmp/snp-');
const home = join(root, 'h');
const prefix = join(root, 'npm');
await mkdir(home);
await writeFile(join(root, 'npmrc'), '');
await writeFile(join(root, 'global-npmrc'), '');
const reservation = createServer();
await new Promise((done, fail) => { reservation.once('error', fail); reservation.listen(0, '127.0.0.1', done); });
const port = reservation.address().port;
await new Promise((done) => reservation.close(done));
// Do not inherit provider credentials, npm credentials, user configuration,
// or a live daemon endpoint. All four scratch runtime roots are explicit.
const env = { PATH: process.env.PATH, HOME: home, SHELL: '/bin/bash',
  SESSIONS_STATE_DIR: join(root, 'r'), SESSIONS_LEDGER_PATH: join(root, 'ledger.sqlite3'), SESSIONS_PORT: String(port),
  npm_config_userconfig: join(root, 'npmrc'), npm_config_globalconfig: join(root, 'global-npmrc'),
  npm_config_cache: join(root, 'cache'), npm_config_update_notifier: 'false', SESSIONS_ACCEPTANCE_FIXTURE: process.env.SESSIONS_ACCEPTANCE_FIXTURE || '' };
const report = { status: 'failed', published_version: version, platform: process.platform, architecture: process.arch,
  native_linux: nativeLinux, harness_source_sha: process.env.SOURCE_SHA || null, isolation_root: root, checks: {},
  excluded: ['systemd service installation', 'OS reboot', 'real provider sign-in', 'GUI', 'native Linux ARM64', 'Linux execution when fixture mode is used'] };

async function command(executable, args, timeout = 120_000) {
  return run(executable, args, { env, cwd: home, timeout, maxBuffer: 1024 * 1024 });
}

try {
  await command('npm', ['install', '--global', '--prefix', prefix, '--ignore-scripts', '--no-audit', '--no-fund',
    '--registry=https://registry.npmjs.org', `@somewhere-tech/sessions@${version}`]);
  const packageRoot = join(prefix, 'lib', 'node_modules', '@somewhere-tech', 'sessions');
  const pkg = JSON.parse(await readFile(join(packageRoot, 'package.json'), 'utf8'));
  assert.equal(pkg.version, version);
  // Explicitly execute the installed package's postinstall: npm versions can
  // require script approval. Do not claim unattended lifecycle approval.
  const install = await command(process.execPath, [join(packageRoot, 'install.cjs')]);
  report.checks.public_package = { version: pkg.version, registry: 'https://registry.npmjs.org', postinstall: 'explicitly executed installed package script', output: install.stdout.trim() };
  const runtime = (await command(process.execPath, ['-e', 'process.stdout.write(require(process.argv[1]).directory())', join(packageRoot, 'lib', 'install.cjs')])).stdout;
  assert.ok(resolve(runtime).startsWith(home + '/'), 'Installed runtime must stay in the scratch home');
  const installedVersion = (await command(join(prefix, 'bin', 'sessions'), ['version'])).stdout.trim();
  assert.ok(installedVersion === version || installedVersion === `v${version}`, 'Published wrapper must run the exact runtime version');
  await command(join(prefix, 'bin', 'sessions'), ['help']);
  report.checks.wrapper = { version: installedVersion, help: 'passed' };
  const runtimeReceipt = `${receiptPath}.runtime.json`;
  await command(process.execPath, [fileURLToPath(new URL('./linux-runtime-acceptance.mjs', import.meta.url)), runtime, runtimeReceipt]);
  const acceptance = JSON.parse(await readFile(runtimeReceipt, 'utf8'));
  assert.equal(acceptance.status, 'passed');
  assert.equal(acceptance.platform, process.platform);
  report.checks.runtime = { receipt: runtimeReceipt, binaries: acceptance.binaries, checks: acceptance.checks, cleanup: acceptance.cleanup };
  report.status = 'passed';
} catch (error) {
  report.error = error.message;
  process.exitCode = 1;
} finally {
  report.retained_state = 'Scratch package, cache, and receipts retained; no user data or existing services modified';
  await mkdir(dirname(receiptPath), { recursive: true });
  await writeFile(receiptPath, `${JSON.stringify(report, null, 2)}\n`);
  console.log(JSON.stringify(report));
}
