// Path-only protection: never read or print the contents of private files.
// Keep shared rules, skills, editor settings, and explicitly named templates usable.
const { execFileSync } = require('node:child_process');

const privatePaths = [
  // MCP clients and copied desktop/CLI configuration; templates have different names.
  /(^|\/)(?:\.mcp|mcp|mcp_config|mcp-config|mcp_settings|cline_mcp_settings)\.json$/i,
  /(^|\/)claude_desktop_config\.json$/i,
  /(^|\/)\.claude\.json(?:\.backup.*)?$/i,
  /(^|\/)\.claude\/(?:settings(?:\.local)?\.json|\.?credentials\.json|history\.jsonl)$/i,
  /(^|\/)\.claude\/(?:projects|sessions|debug|logs|file-history|shell-snapshots|session-env|todos)(?:\/|$)/i,

  // Codex user state must stay outside the repo; runtime config uses templates here.
  /(^|\/)\.codex\/(?:config\.toml|auth\.json|history\.jsonl|\.codex-global-state\.json|rules\/default\.rules)$/i,
  /(^|\/)\.codex\/(?:sessions|archived_sessions|log|logs|shell_snapshots|memories|memories_v2|tmp|sqlite|cache|plugins\/cache)(?:\/|$)/i,
  /(^|\/)\.codex\/[^/]+\.(?:sqlite|sqlite3|db)(?:-(?:wal|shm))?$/i,

  /(^|\/)\.cursor\/(?:cli(?:-config)?\.json|auth\.json|history\.jsonl)$/i,
  /(^|\/)\.cursor\/(?:chats|conversations|history|sessions|logs)(?:\/|$)/i,
  /(^|\/)\.gemini\/(?:settings\.json|oauth_creds\.json|google_accounts\.json|mcp-oauth-tokens\.json|trustedFolders\.json)$/i,
  /(^|\/)\.gemini\/(?:tmp|history|sessions|logs)(?:\/|$)/i,
  /(^|\/)\.continue\/config\.(?:json|ya?ml|ts)$/i,
  /(^|\/)\.continue\/(?:sessions|logs|dev_data|index|\.cache)(?:\/|$)/i,
  /(^|\/)\.cline\/data(?:\/|$)/i,
  /(^|\/)\.(?:cline|roo)\/(?:sessions|tasks|logs)(?:\/|$)/i,
  /(^|\/)(?:\.opencode|opencode)\/auth\.json$/i,
  /(^|\/)opencode(?:\.local)?\.jsonc?$/i,
  /(^|\/)\.opencode\/(?:logs|sessions|storage)(?:\/|$)/i,
  /(^|\/)\.copilot\/(?:config\.json$|(?:session-state|logs)(?:\/|$))/i,
  /(^|\/)\.aider\.(?:conf\.ya?ml|[^/]*history[^/]*|tags\.cache[^/]*)(?:\/|$)/i,
  /(^|\/)\.specstory\/history(?:\/|$)/i,
  /(^|\/)\.antigravitycli(?:\/|$)/i,

  // Existing local secret files must also stay out when added with git add -f.
  /(^|\/)\.env(?:\.[^/]*)?\.local$/i,
  /(^|\/)\.env(?:\.docker)?$/i,
  /(^|\/)gcp-key\.json$/i,
];

try {
  // The full index also catches unchanged private files and alternate commit indexes.
  // NUL delimiters preserve spaces/newlines; no worktree files or link targets are read.
  const paths = execFileSync('git', ['ls-files', '--cached', '--full-name', '-z', '--', ':/'], {
    maxBuffer: 16 * 1024 * 1024,
    stdio: ['ignore', 'pipe', 'pipe'],
  }).toString('utf8').split('\0').filter(Boolean);
  const blocked = [...new Set(paths)].filter((name) => privatePaths.some((rule) => rule.test(name)));
  if (blocked.length > 0) {
    console.error('Commit blocked: private settings, credentials, or agent history are in the Git index:');
    for (const name of blocked) console.error(`  ${JSON.stringify(name)}`);
    console.error('Keep these files local and remove them from the index. Share an example/template instead.');
    process.exitCode = 1;
  }
} catch {
  console.error('Commit blocked: unable to inspect the Git index. Run this check inside the repository.');
  process.exitCode = 1;
}
