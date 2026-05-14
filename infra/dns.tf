# Apex → GCS bucket (CNAME flattening: Cloudflare auto-flattens CNAMEs on apex)
resource "cloudflare_record" "apex" {
  zone_id = var.cloudflare_zone_id
  name    = "@"
  type    = "CNAME"
  content = "c.storage.googleapis.com"
  proxied = true
  ttl     = 1 # Auto when proxied
}

# www → apex redirect (Cloudflare handles this via a redirect rule below)
resource "cloudflare_record" "www" {
  zone_id = var.cloudflare_zone_id
  name    = "www"
  type    = "CNAME"
  content = "c.storage.googleapis.com"
  proxied = true
  ttl     = 1
}

# API traffic is handled by a Cloudflare Worker on formbuddy.fit/api/*
# that proxies to Cloud Run. This avoids CORS (same origin) and the Host
# header issue (Cloud Run domain mapping unavailable in asia-northeast3,
# and Origin Rules require a paid Cloudflare plan).
#
# Worker setup (one-time, via Cloudflare dashboard):
#   Workers & Pages → Create Worker → "formbuddy-api-proxy"
#   Route: formbuddy.fit/api/*  Zone: formbuddy.fit

# www → apex 301 redirect
# NOTE: Set this up manually in the Cloudflare dashboard:
#   Rules → Redirect Rules → Create Rule
#   When: hostname equals "www.formbuddy.fit"
#   Then: Dynamic redirect to concat("https://formbuddy.fit", http.request.uri.path), status 301
# Both @ and www already point to the same GCS bucket, so the SPA works on both
# even without the redirect rule.
