import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
import { artifactNames, findReusableArtifacts, stampVersion, verifyAssets, writeManifest } from './release.mjs';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const commit = 'a'.repeat(40);
function fixture(t) {
  const path = mkdtempSync(join(tmpdir(), 'rex-release-'));
  t.after(() => rmSync(path, { recursive: true, force: true }));
  return path;
}

test('version stamping keeps all app manifests consistent and rejects invalid input without writes', t => {
  const directory = fixture(t);
  mkdirSync(join(directory, 'src-tauri'));
  const files = ['package.json', 'src-tauri/tauri.conf.json', 'src-tauri/Cargo.toml', 'src-tauri/Cargo.lock'];
  for (const file of files) writeFileSync(join(directory, file), readFileSync(join(root, file)));
  const before = files.map(file => readFileSync(join(directory, file), 'utf8'));
  for (const invalid of ['v1.2.3', '../1.2.3', '01.2.3', '1.2.3\n', '1.2.3-preview.1']) assert.throws(() => stampVersion(invalid, directory));
  assert.deepEqual(files.map(file => readFileSync(join(directory, file), 'utf8')), before);
  stampVersion('2.3.4', directory);
  assert.equal(JSON.parse(readFileSync(join(directory, 'package.json'))).version, '2.3.4');
  assert.equal(JSON.parse(readFileSync(join(directory, 'src-tauri/tauri.conf.json'))).version, '2.3.4');
  assert.match(readFileSync(join(directory, 'src-tauri/Cargo.toml'), 'utf8'), /^version = "2.3.4"$/m);
  assert.match(readFileSync(join(directory, 'src-tauri/Cargo.lock'), 'utf8'), /name = "rex-desktop"\r?\nversion = "2.3.4"/);
});

test('publication requires both platforms from the same version and commit, with intact files and no private keys', t => {
  const directory = fixture(t);
  for (const platform of ['windows-x64', 'macos-arm64']) {
    for (const name of artifactNames(platform, '1.2.3')) writeFileSync(join(directory, name), name);
    writeManifest(platform, '1.2.3', commit, directory);
  }
  assert.equal(verifyAssets('1.2.3', commit, directory).trim().split('\n').length, 3);
  assert.throws(() => verifyAssets('1.2.3', 'b'.repeat(40), directory), /metadata mismatch/);
  const dmg = join(directory, artifactNames('macos-arm64', '1.2.3')[0]);
  const original = readFileSync(dmg);
  writeFileSync(dmg, 'modified after validation');
  assert.throws(() => verifyAssets('1.2.3', commit, directory), /checksum mismatch/);
  writeFileSync(dmg, original);
  writeFileSync(join(directory, 'private-key.pfx'), 'never publish');
  assert.throws(() => verifyAssets('1.2.3', commit, directory), /Unexpected release asset/);
  rmSync(join(directory, 'private-key.pfx'));
  rmSync(dmg);
  assert.throws(() => verifyAssets('1.2.3', commit, directory), /ENOENT/);
});

for (const [message, expected, tagged = true] of [
  ['fix(stream): retry overloaded models', '0.1.1'],
  ['feat(windows): add native desktop support', '0.2.0'],
  ['feat(api)!: change the endpoint contract', '1.0.0'],
  ['fix(api): change responses\n\nBREAKING CHANGE: response fields were renamed', '1.0.0'],
  ['docs: clarify preview signing', undefined],
  ['feat: initial desktop release', '1.0.0', false],
]) {
  test(`semantic-release plans ${expected ?? 'no release'} for ${message.split('\n')[0]} without creating a tag`, async t => {
    const directory = fixture(t);
    const remote = join(directory, 'remote.git');
    const source = join(directory, 'source');
    const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('GITHUB_') && !key.startsWith('GH_') && key !== 'CI'));
    const git = (...args) => execFileSync('git', args, { cwd: source, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
    execFileSync('git', ['init', '--bare', '--initial-branch=main', remote], { env, stdio: 'ignore' });
    mkdirSync(source);
    git('init', '--initial-branch=main');
    git('config', 'user.name', 'Release test');
    git('config', 'user.email', 'release-test@example.invalid');
    git('remote', 'add', 'origin', 'https://github.com/rex-release-test/rex.git');
    git('config', `url.${pathToFileURL(remote).href}.insteadOf`, 'https://github.com/rex-release-test/rex.git');
    git('commit', '--allow-empty', '-m', 'chore: initial source');
    if (tagged) git('tag', 'v0.1.0');
    git('commit', '--allow-empty', '-m', message);
    git('push', '--tags', 'origin', 'main');
    symlinkSync(join(root, 'node_modules'), join(source, 'node_modules'), 'junction');
    execFileSync(process.execPath, [join(root, 'scripts/release.mjs'), 'plan'], { cwd: source, env: { ...env, NODE_TEST_CONTEXT: '' }, stdio: ['ignore', 'pipe', 'pipe'] });
    let version;
    try { version = JSON.parse(readFileSync(join(source, 'release-plan.json'), 'utf8')).version; } catch (error) { if (error.code !== 'ENOENT') throw error; }
    assert.equal(version, expected);
    assert.equal(git('tag', '-l'), tagged ? 'v0.1.0' : '');
    assert.equal(git('ls-remote', '--tags', 'origin').split('\n').filter(Boolean).length, tagged ? 1 : 0);
  });
}

function artifactFixture(platform, run = {}) {
  return { name: `rex-release-installer-${platform}-1.2.3-${commit}`, expired: false,
    workflow_run: { id: 100, head_branch: 'main', head_sha: commit, ...run } };
}
const trustedRun = { id: 100, head_branch: 'main', head_sha: commit, status: 'completed',
  event: 'push', path: '.github/workflows/release.yml' };
function discover(artifacts, runs = {}, options = {}) {
  return findReusableArtifacts({ repository: 'helpin-labs/rex', version: '1.2.3', commit,
    runId: '200', token: 'fixture-token', ...options,
    request: async (url, init) => {
      assert.equal(init.redirect, 'error');
      assert.equal(init.headers.Authorization, 'Bearer fixture-token');
      if (url.includes('artifacts?')) {
        const name = new URL(url).searchParams.get('name');
        return { ok: true, json: async () => ({ artifacts: artifacts.filter(a => a.name === name) }) };
      }
      const id = url.split('/').at(-1);
      assert.ok(runs[id], `Unexpected run lookup: ${id}`);
      return { ok: true, json: async () => runs[id] };
    },
  });
}

test('reuse discovers both exact-source final-version platforms from trusted completed main builds', async () => {
  const result = await discover([artifactFixture('windows-x64'), artifactFixture('macos-arm64')], { 100: trustedRun });
  assert.deepEqual(result, { windows_run: '100', macos_run: '100' });
});

test('a prior failed overall release can supply its validated platform while the missing platform builds', async () => {
  assert.deepEqual(await discover([artifactFixture('macos-arm64')], { 100: { ...trustedRun, conclusion: 'failure' } }),
    { windows_run: '', macos_run: '100' });
});

test('reuse ignores expired, different-source, different-version, PR, and current-run artifacts', async () => {
  const artifacts = [
    { ...artifactFixture('windows-x64'), expired: true },
    artifactFixture('windows-x64', { head_sha: 'b'.repeat(40) }),
    artifactFixture('windows-x64', { head_branch: 'feature' }),
    artifactFixture('windows-x64', { id: 200 }),
    { ...artifactFixture('windows-x64'), name: `rex-release-installer-windows-x64-9.9.9-${commit}` },
  ];
  assert.deepEqual(await discover(artifacts), { windows_run: '', macos_run: '' });
});

for (const invalid of [
  { head_branch: 'feature' }, { head_sha: 'b'.repeat(40) }, { status: 'in_progress' },
  { event: 'pull_request' }, { event: 'pull_request_target' }, { path: '.github/workflows/unrelated.yml' },
  { path: '.github/workflows/macos.yml' },
]) {
  test(`reuse rejects a Windows run with ${JSON.stringify(invalid)}`, async () => {
    assert.deepEqual(await discover([artifactFixture('windows-x64')], { 100: { ...trustedRun, ...invalid } }),
      { windows_run: '', macos_run: '' });
  });
}

test('artifact discovery fails clearly on API failures instead of silently triggering expensive rebuilds', async () => {
  await assert.rejects(findReusableArtifacts({ repository: 'helpin-labs/rex', version: '1.2.3', commit,
    token: 'fixture-token', request: async () => ({ ok: false, status: 403 }) }), /HTTP 403/);
});

const releaseWorkflow = readFileSync(join(root, '.github/workflows/release.yml'), 'utf8');
function jobCondition(job, needs, cancelled = false, workflow = releaseWorkflow) {
  const block = workflow.replace(/\r\n/g, '\n').split(`\n  ${job}:\n`)[1].split(/\n  [a-z]+:\n/)[0];
  const expression = block.match(/    if: >-\n([\s\S]*?)\n    runs-on:/)?.[1].trim()
    ?? block.match(/    if: (.*)/)[1];
  return Boolean(new Function('needs', 'github', 'always', 'cancelled', `return (${expression})`)(
    needs, { ref: 'refs/heads/main' }, () => true, () => cancelled));
}
function releaseNeeds(windowsResult, macosResult, windowsRun = '', macosRun = '', version = '1.2.3') {
  return { version: { result: 'success', outputs: { version, windows_run: windowsRun, macos_run: macosRun } },
    windows: { result: windowsResult }, macos: { result: macosResult } };
}

for (const lineEnding of ['LF', 'CRLF']) {
  const workflow = releaseWorkflow.replace(/\r?\n/g, lineEnding === 'LF' ? '\n' : '\r\n');
  test(`release job conditions skip unnecessary builds and publish only two available validated platforms (${lineEnding})`, () => {
    const condition = (job, needs, cancelled = false) => jobCondition(job, needs, cancelled, workflow);
    const none = releaseNeeds('skipped', 'skipped', '', '', '');
    for (const job of ['windows', 'macos', 'publish']) assert.equal(condition(job, none), false);
    const fresh = releaseNeeds('success', 'success');
    for (const job of ['windows', 'macos', 'publish']) assert.equal(condition(job, fresh), true);
    const reused = releaseNeeds('skipped', 'skipped', '100', '101');
    assert.equal(condition('windows', reused), false);
    assert.equal(condition('macos', reused), false);
    assert.equal(condition('publish', reused), true);
    assert.equal(condition('publish', releaseNeeds('success', 'skipped', '', '101')), true);
    assert.equal(condition('publish', releaseNeeds('skipped', 'success', '100')), true);
    for (const result of ['failure', 'cancelled', 'skipped']) {
      assert.equal(condition('publish', releaseNeeds(result, 'success')), false);
      assert.equal(condition('publish', releaseNeeds('success', result)), false);
    }
    assert.equal(condition('publish', reused, true), false);
  });
}
