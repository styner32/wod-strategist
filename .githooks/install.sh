#!/bin/sh
set -eu

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

configured_hooks=$(git config --get core.hooksPath || :)
if [ -n "$configured_hooks" ] && [ "$configured_hooks" != '.githooks' ]; then
  echo 'Hooks not installed: core.hooksPath already points elsewhere. Integrate the existing hooks first.' >&2
  exit 1
fi

if [ -z "$configured_hooks" ]; then
  hooks_dir=$(git rev-parse --git-path hooks)
  for hook in "$hooks_dir"/*; do
    case "$hook" in *.sample) continue ;; esac
    if [ -f "$hook" ] && [ -x "$hook" ]; then
      echo 'Hooks not installed: an existing executable Git hook would be hidden.' >&2
      exit 1
    fi
  done
fi

if [ ! -x .githooks/pre-commit ]; then
  echo 'Hooks not installed: .githooks/pre-commit must be executable.' >&2
  exit 1
fi

node scripts/check-private-files.cjs
git config --local core.hooksPath .githooks
echo 'Git hooks installed for this clone. Private file paths will block commits.'
