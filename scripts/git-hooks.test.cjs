const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { test } = require('node:test');

const sourceRoot = path.resolve(__dirname, '..');
const dummyContent = 'PRIVATE_FIXTURE_VALUE_MUST_NOT_APPEAR_IN_OUTPUT\n';

function fixture(t, install = true) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'wod-git-hooks-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  // No ambient Git configuration, credentials, signing, templates, or hooks.
  const env = {
    PATH: process.env.PATH,
    TMPDIR: root,
    GIT_CONFIG_NOSYSTEM: '1',
    GIT_CONFIG_GLOBAL: '/dev/null',
    GIT_TERMINAL_PROMPT: '0',
    GIT_OPTIONAL_LOCKS: '0',
    GIT_AUTHOR_NAME: 'Hook Test',
    GIT_AUTHOR_EMAIL: 'hook-test@example.invalid',
    GIT_COMMITTER_NAME: 'Hook Test',
    GIT_COMMITTER_EMAIL: 'hook-test@example.invalid',
    LC_ALL: 'C',
  };
  function run(command, args, options = {}) {
    const result = spawnSync(command, args, {
      cwd: root, env, encoding: 'utf8', timeout: 10000, ...options,
    });
    assert.ifError(result.error);
    return { ...result, output: result.stdout + result.stderr };
  }
  function git(...args) { return run('git', args); }
  function ok(result) {
    assert.equal(result.status, 0, result.output);
    return result;
  }
  function write(name, content = dummyContent) {
    const target = path.join(root, name);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, content);
  }
  function blocked(result, names = []) {
    assert.notEqual(result.status, 0, result.output);
    assert.match(result.output, /Commit blocked:/);
    for (const name of names) assert.ok(result.output.includes(JSON.stringify(name)), result.output);
    assert.ok(!result.output.includes(dummyContent.trim()), 'Private contents must not be printed');
  }
  ok(git('init', '--quiet', '--template='));
  for (const name of ['.githooks/pre-commit', '.githooks/install.sh', 'scripts/check-private-files.cjs']) {
    const target = path.join(root, name);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.copyFileSync(path.join(sourceRoot, name), target);
  }
  fs.chmodSync(path.join(root, '.githooks/pre-commit'), 0o755);
  if (install) ok(run('sh', ['.githooks/install.sh']));
  return { root, env, run, git, ok, write, blocked };
}

test('real commits reject known private paths, including forced and nested additions', (t) => {
  const f = fixture(t);
  const names = [
    '.claude/settings.local.json', '.claude/settings.json', '.claude/.credentials.json',
    '.claude/projects/session/transcript.jsonl', '.claude.json', 'claude_desktop_config.json',
    '.codex/config.toml', '.codex/auth.json', '.codex/history.jsonl',
    '.codex/sessions/session.jsonl', '.codex/rules/default.rules', '.codex/state_5.sqlite-wal',
    '.cursor/mcp.json', '.cursor/cli-config.json', '.gemini/settings.json',
    '.gemini/oauth_creds.json', '.gemini/tmp/project/shell_history',
    '.continue/config.yaml', '.continue/.env', '.continue/sessions/session.json',
    '.cline/data/settings/providers.json', '.roo/mcp.json', '.windsurf/mcp_config.json',
    '.copilot/mcp-config.json', '.copilot/config.json', '.copilot/session-state/session/events.jsonl',
    '.aider.conf.yml', '.aider.chat.history.md', '.aider.input.history',
    '.specstory/history/session.md', 'opencode.jsonc', '.opencode/auth.json',
    '.mcp.json', '.vscode/mcp.json', '.agents/plugins/stitch/mcp_config.json',
    'api/.cursor/mcp.json', 'api/.env', 'api/.env.docker', '.env.local', '.env.live.local',
    'api/gcp-key.json', '.antigravitycli/state.json',
    'space tab\tline\nfolder/.CLAUDE/SETTINGS.LOCAL.JSON',
  ];
  f.write('.gitignore', '.claude/\n.env*\n');
  for (const name of names) f.write(name);
  f.ok(f.git('add', '-f', '--', '.gitignore', ...names));
  const before = f.ok(f.git('ls-files', '--stage', '-z')).stdout;
  f.blocked(f.git('commit', '-m', 'must be blocked'), names);
  assert.equal(f.ok(f.git('ls-files', '--stage', '-z')).stdout, before, 'Hook must not mutate the index');
});

test('shared editor settings, instructions, skills, and templates can be committed', (t) => {
  const f = fixture(t);
  const names = [
    '.vscode/settings.json', '.vscode/extensions.json', 'AGENTS.md', 'CLAUDE.md', 'GEMINI.md',
    '.cursor/rules/project.mdc', '.agents/skills/example/SKILL.md', '.claude/skills/example/SKILL.md',
    '.claude/settings.example.json', '.codex/config.example.toml', '.cursor/mcp.example.json',
    '.gemini/settings.example.json', '.continue/config.example.yaml', '.continue/.env.example',
    '.copilot/config.json.example',
    '.agents/plugins/stitch/mcp_config.example.json', 'opencode.example.json',
    'api/.env.example', 'api/.env.test', 'web/.env.production', 'src/settings.json', 'src/auth.json',
  ];
  for (const name of names) f.write(name, 'shared fixture\n');
  f.ok(f.git('add', '--', ...names));
  f.ok(f.git('commit', '-m', 'shared configuration'));
  f.ok(f.run('sh', ['.githooks/install.sh'])); // Reinstallation is idempotent.
});

test('private files not in the commit index do not block safe commits', (t) => {
  const f = fixture(t);
  f.write('.claude/settings.local.json');
  f.write('README.md', 'safe\n');
  f.ok(f.git('add', 'README.md'));
  f.ok(f.git('commit', '-m', 'safe with local settings'));
});

test('staged private files stay blocked after removal from the worktree', (t) => {
  const f = fixture(t);
  const name = '.cursor/mcp.json';
  f.write(name);
  f.ok(f.git('add', name));
  fs.unlinkSync(path.join(f.root, name));
  f.blocked(f.git('commit', '-m', 'index still contains the file'), [name]);
});

test('unchanged tracked private files block commits but staged removal is allowed', (t) => {
  const f = fixture(t);
  const name = '.gemini/settings.json';
  f.write(name);
  f.ok(f.git('add', name));
  // Only this disposable fixture deliberately seeds an old tracked private file.
  f.ok(f.git('-c', 'core.hooksPath=/dev/null', 'commit', '-m', 'old fixture'));
  f.write('README.md', 'safe\n');
  f.ok(f.git('add', 'README.md'));
  f.blocked(f.git('commit', '-m', 'unchanged private file'), [name]);
  f.ok(f.git('rm', '--cached', name));
  f.ok(f.git('commit', '-m', 'remove private file'));
  assert.equal(fs.readFileSync(path.join(f.root, name), 'utf8'), dummyContent);
});

test('renaming into a private path is blocked and renaming out is allowed', (t) => {
  const f = fixture(t);
  f.write('safe.json', 'safe fixture\n');
  f.ok(f.git('add', 'safe.json'));
  f.ok(f.git('commit', '-m', 'safe initial file'));
  fs.mkdirSync(path.join(f.root, '.cursor'));
  f.ok(f.git('mv', 'safe.json', '.cursor/mcp.json'));
  f.blocked(f.git('commit', '-m', 'private rename'), ['.cursor/mcp.json']);
  f.ok(f.git('mv', '.cursor/mcp.json', '.cursor/mcp.example.json'));
  f.ok(f.git('commit', '-m', 'template rename'));
});

test('checks from subdirectories still cover the entire index', (t) => {
  const f = fixture(t);
  f.write('.claude/settings.local.json');
  f.ok(f.git('add', '-f', '.claude/settings.local.json'));
  const options = { cwd: path.join(f.root, 'scripts') };
  f.blocked(f.run(process.execPath, ['check-private-files.cjs'], options), ['.claude/settings.local.json']);
  f.blocked(f.run('git', ['-c', 'diff.relative=true', 'commit', '-m', 'subdirectory'], options));
});

test('commit --only honors its alternate index instead of unrelated staged files', (t) => {
  const f = fixture(t);
  f.write('README.md', 'initial\n');
  f.ok(f.git('add', 'README.md'));
  f.ok(f.git('commit', '-m', 'initial'));
  f.write('.codex/auth.json');
  f.ok(f.git('add', '.codex/auth.json'));
  f.write('README.md', 'updated\n');
  f.ok(f.git('commit', '--only', 'README.md', '-m', 'safe subset'));
  assert.equal(f.git('cat-file', '-e', 'HEAD:.codex/auth.json').status, 128);
  f.blocked(f.git('commit', '-m', 'remaining private file'), ['.codex/auth.json']);
});

test('amended and commit -a operations run the path guard', (t) => {
  const f = fixture(t);
  f.write('README.md', 'initial\n');
  f.ok(f.git('add', 'README.md'));
  f.ok(f.git('commit', '-m', 'initial'));
  f.write('.mcp.json');
  f.ok(f.git('add', '.mcp.json'));
  f.blocked(f.git('commit', '--amend', '--no-edit'), ['.mcp.json']);
  f.write('README.md', 'updated\n');
  f.blocked(f.git('commit', '-am', 'all changes'), ['.mcp.json']);
});

test('private symlinks are blocked without opening their targets', (t) => {
  const f = fixture(t);
  fs.mkdirSync(path.join(f.root, '.claude'));
  fs.symlinkSync('missing-private-target', path.join(f.root, '.claude/settings.local.json'));
  f.ok(f.git('add', '-f', '.claude/settings.local.json'));
  f.blocked(f.git('commit', '-m', 'private symlink'), ['.claude/settings.local.json']);
});

test('installation preserves a different configured hooks directory', (t) => {
  const f = fixture(t, false);
  f.ok(f.git('config', '--local', 'core.hooksPath', '.existing-hooks'));
  const result = f.run('sh', ['.githooks/install.sh']);
  assert.notEqual(result.status, 0);
  assert.match(result.output, /already points elsewhere/);
  assert.equal(f.ok(f.git('config', '--get', 'core.hooksPath')).stdout.trim(), '.existing-hooks');
});

test('installation refuses to hide any existing executable default hook', (t) => {
  const f = fixture(t, false);
  f.write('.git/hooks/pre-push', '#!/bin/sh\nexit 0\n');
  fs.chmodSync(path.join(f.root, '.git/hooks/pre-push'), 0o755);
  const result = f.run('sh', ['.githooks/install.sh']);
  assert.notEqual(result.status, 0);
  assert.match(result.output, /existing executable Git hook/);
  assert.equal(f.git('config', '--get', 'core.hooksPath').status, 1);
});

test('installation checks the index before changing Git configuration', (t) => {
  const f = fixture(t, false);
  f.write('.claude/settings.local.json');
  f.ok(f.git('add', '-f', '.claude/settings.local.json'));
  f.blocked(f.run('sh', ['.githooks/install.sh']));
  assert.equal(f.git('config', '--get', 'core.hooksPath').status, 1);
});

test('missing Node.js, checker, or Git index inspection fails closed', (t) => {
  const f = fixture(t);
  const withoutNode = f.run('/bin/sh', ['.githooks/pre-commit'], { env: { PATH: '' } });
  assert.notEqual(withoutNode.status, 0);
  assert.match(withoutNode.output, /Node.js is required/);
  f.write('README.md', 'safe\n');
  f.ok(f.git('add', 'README.md'));
  fs.unlinkSync(path.join(f.root, 'scripts/check-private-files.cjs'));
  assert.notEqual(f.git('commit', '-m', 'checker missing').status, 0);
  const outside = fs.mkdtempSync(path.join(os.tmpdir(), 'wod-hook-outside-'));
  t.after(() => fs.rmSync(outside, { recursive: true, force: true }));
  f.blocked(f.run(process.execPath, [path.join(sourceRoot, 'scripts/check-private-files.cjs')], { cwd: outside }));
});
