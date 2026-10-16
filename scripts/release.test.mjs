import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
import { artifactNames, stampVersion, verifyAssets, writeManifest } from './release.mjs';

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
