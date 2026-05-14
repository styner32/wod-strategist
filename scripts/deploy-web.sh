#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WEB_DIR="$SCRIPT_DIR/../web"
WORKER_DIR="$SCRIPT_DIR/../infra/worker"
BUCKET="gs://formbuddy.fit"

echo "==> Building web SPA..."
cd "$WEB_DIR"
npm run build

echo "==> Syncing dist/ to $BUCKET..."
gsutil -m rsync -d -r dist/ "$BUCKET"

echo "==> Setting cache headers for hashed assets..."
gsutil -m setmeta -h "Cache-Control:public, max-age=31536000, immutable" \
  "${BUCKET}/assets/**"

echo "==> Setting no-cache on index.html..."
gsutil -m setmeta -h "Cache-Control:no-cache" "${BUCKET}/index.html"

echo "==> Deploying Cloudflare Worker (API proxy)..."
cd "$WORKER_DIR"
npx wrangler deploy

echo ""
echo "Done! If you updated index.html, purge the Cloudflare cache:"
echo "  Dashboard → Caching → Configuration → Purge Custom → /index.html"

