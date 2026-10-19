import { appendFileSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';

export function validateVersion(version) {
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version)) throw new Error('Expected a stable semantic version');
  return version;
}

// Only completed main runs of these workflows can supply validated installers.
export async function findReusableArtifacts({ repository, version, commit, runId, token, request = fetch }) {
  validateVersion(version);
  if (!/^[\w.-]+\/[\w.-]+$/.test(repository) || !/^[0-9a-f]{40}$/.test(commit)) throw new Error('Invalid build identity');
  const get = async path => {
    const response = await request(`https://api.github.com/repos/${repository}/actions/${path}`, {
      headers: { Authorization: `Bearer ${token}`, Accept: 'application/vnd.github+json' }, redirect: 'error',
    });
    if (!response.ok) throw new Error(`Artifact discovery failed: HTTP ${response.status}`);
    return response.json();
  };
  const result = {};
  for (const [platform, output, workflow] of [
    ['windows-x64', 'windows_run', 'windows.yml'], ['macos-arm64', 'macos_run', 'macos.yml'],
  ]) {
    result[output] = '';
    const name = `rex-release-installer-${platform}-${version}-${commit}`;
    const { artifacts } = await get(`artifacts?name=${encodeURIComponent(name)}&per_page=100`);
    for (const artifact of artifacts) {
      const source = artifact.workflow_run;
      if (artifact.name !== name || artifact.expired || !source || source.head_branch !== 'main' || source.head_sha !== commit || String(source.id) === String(runId)) continue;
      const run = await get(`runs/${source.id}`);
      if (run.status !== 'completed' || run.head_branch !== 'main' || run.head_sha !== commit || !['push', 'workflow_dispatch'].includes(run.event)) continue;
      if (!['.github/workflows/release.yml', `.github/workflows/${workflow}`].includes(run.path)) continue;
      result[output] = String(run.id);
      break;
    }
  }
  return result;
}

export function stampVersion(version, root = process.cwd()) {
  validateVersion(version);
  const updates = new Map();
  for (const file of ['package.json', 'src-tauri/tauri.conf.json']) {
    const value = JSON.parse(readFileSync(join(root, file), 'utf8'));
    value.version = version;
    updates.set(file, JSON.stringify(value, null, 2) + '\n');
  }
  for (const [file, pattern] of [
    ['src-tauri/Cargo.toml', /^(version\s*=\s*")[^"]+("\s*)$/gm],
    ['src-tauri/Cargo.lock', /(\[\[package\]\]\r?\nname = "rex-desktop"\r?\nversion = ")[^"]+(")/g],
  ]) {
    const text = readFileSync(join(root, file), 'utf8');
    if ([...text.matchAll(pattern)].length !== 1) throw new Error(`Expected one application version in ${file}`);
    updates.set(file, text.replace(pattern, (_, prefix, suffix) => prefix + version + suffix));
  }
  for (const [file, text] of updates) writeFileSync(join(root, file), text);
}

export async function planRelease(cwd = process.cwd(), env = process.env) {
  const { default: semanticRelease } = await import('semantic-release');
  return semanticRelease({
    branches: ['main'],
    tagFormat: 'v${version}',
    dryRun: true,
    plugins: [
      ['@semantic-release/commit-analyzer', { preset: 'conventionalcommits' }],
      ['@semantic-release/release-notes-generator', { preset: 'conventionalcommits' }],
    ],
  }, { cwd, env });
}

export function artifactNames(platform, version) {
  validateVersion(version);
  switch (platform) {
    case 'windows-x64': return [`Rex-${version}-windows-x64-setup.exe`, `Rex-${version}-windows-x64-preview.cer`];
    case 'macos-arm64': return [`Rex-${version}-macos-arm64.dmg`];
    default: throw new Error('Unsupported release platform');
  }
}

function validateCommit(commit) {
  if (!/^[0-9a-f]{40}$/.test(commit)) throw new Error('Expected a full source commit');
}

function sha256(path) { return createHash('sha256').update(readFileSync(path)).digest('hex'); }

export function writeManifest(platform, version, commit, directory = 'release-assets') {
  validateCommit(commit);
  const files = artifactNames(platform, version).map(name => ({ name, sha256: sha256(join(directory, name)) }));
  const manifest = { platform, version, commit, signing: platform === 'windows-x64' ? 'self-signed' : 'ad-hoc', files };
  writeFileSync(join(directory, `${platform}.json`), JSON.stringify(manifest, null, 2) + '\n');
}

export function verifyAssets(version, commit, directory = 'release-assets') {
  validateVersion(version);
  validateCommit(commit);
  const expected = new Set();
  const sums = [];
  for (const platform of ['windows-x64', 'macos-arm64']) {
    const manifestName = `${platform}.json`;
    expected.add(manifestName);
    const manifest = JSON.parse(readFileSync(join(directory, manifestName), 'utf8'));
    const signing = platform === 'windows-x64' ? 'self-signed' : 'ad-hoc';
    if (manifest.platform !== platform || manifest.version !== version || manifest.commit !== commit || manifest.signing !== signing) {
      throw new Error(`Release metadata mismatch: ${platform}`);
    }
    const names = artifactNames(platform, version);
    if (!Array.isArray(manifest.files) || manifest.files.length !== names.length) throw new Error(`Missing assets: ${platform}`);
    for (const name of names) {
      expected.add(name);
      const records = manifest.files.filter(file => file.name === name);
      if (records.length !== 1 || records[0].sha256 !== sha256(join(directory, name))) throw new Error(`Artifact checksum mismatch: ${name}`);
      sums.push(`${records[0].sha256}  ${name}`);
    }
  }
  if (readdirSync(directory).some(name => !expected.has(name))) throw new Error('Unexpected release asset; refusing to publish');
  return sums.sort().join('\n') + '\n';
}

const previewNotice = `These are development-signed preview installers. Windows uses a self-signed certificate generated for this build; its public certificate is included. macOS uses ad-hoc signing and is not notarized. Neither signature establishes a trusted publisher on another computer. See docs/RELEASING.md for verification and installation instructions.`;

async function main() {
  const [command, value, version] = process.argv.slice(2);
  if (command === 'stamp') return stampVersion(value);
  if (command === 'manifest') {
    return writeManifest(value, version, execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim());
  }
  if (command === 'plan') {
    // shortcut: retry a failed publish job in its original run; whole-run draft-tag recovery is not implemented.
    const result = await planRelease();
    const next = result ? validateVersion(result.nextRelease.version) : '';
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, `version=${next}\n`);
    if (next) writeFileSync('release-plan.json', JSON.stringify({ version: next, commit: result.nextRelease.gitHead, notes: result.nextRelease.notes }, null, 2) + '\n');
    return;
  }
  if (command === 'reuse') {
    const result = await findReusableArtifacts({ repository: process.env.GITHUB_REPOSITORY, version: value,
      commit: process.env.GITHUB_SHA, runId: process.env.GITHUB_RUN_ID, token: process.env.GITHUB_TOKEN });
    for (const [key, id] of Object.entries(result)) appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${id}\n`);
    return;
  }
  if (command === 'verify') {
    const plan = JSON.parse(readFileSync('release-plan.json', 'utf8'));
    const head = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
    if (plan.version !== value || plan.commit !== head || head !== process.env.GITHUB_SHA || typeof plan.notes !== 'string') throw new Error('Release plan does not match this checkout');
    const sums = verifyAssets(plan.version, head);
    writeFileSync('release-assets/SHA256SUMS', sums);
    writeFileSync('release-notes.md', `${plan.notes}\n\n### Preview signing\n\n${previewNotice}\n`);
    return;
  }
  throw new Error('Usage: release.mjs plan | stamp VERSION | manifest PLATFORM VERSION | reuse VERSION | verify VERSION');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await main();
