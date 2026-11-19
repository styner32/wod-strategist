DROP INDEX IF EXISTS idx_sessions_profile_idempotency_key;

DELETE FROM sessions
WHERE idempotency_key LIKE 'backfill:%'
  AND effort IS NULL;
