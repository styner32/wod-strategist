-- 1. Backfill missing sessions from analysis_results (richest source: status, wod_description, timestamps).
-- Keep wod_evaluation_version = 0 so legacy records do not suddenly become effort_missing.
INSERT INTO sessions (
    session_id,
    profile_id,
    status,
    idempotency_key,
    wod_description,
    workout_type,
    movement_hints,
    wod_evaluation_version,
    created_at,
    updated_at
)
SELECT
    ar.session_id,
    ar.profile_id,
    CASE COALESCE(UPPER(TRIM(ar.status)), '')
        WHEN 'COMPLETED' THEN 'completed'
        WHEN 'FAILED' THEN 'failed'
        ELSE 'started'
    END,
    'backfill:' || ar.session_id,
    COALESCE(ar.wod_description, ''),
    CASE
        WHEN ar.session_id ~* '^(P[0-9]+-)?WARMUP-' THEN 'warmup'
        WHEN ar.session_id ~* '^(P[0-9]+-)?ACCESSORY-' THEN 'accessory'
        WHEN ar.session_id ~* '^(P[0-9]+-)?COOLDOWN-' THEN 'cooldown'
        ELSE 'wod'
    END,
    '[]'::jsonb,
    0,
    COALESCE(ar.created_at, NOW()),
    COALESCE(ar.updated_at, ar.created_at, NOW())
FROM analysis_results ar
WHERE TRIM(ar.session_id) <> ''
  AND NOT EXISTS (
      SELECT 1 FROM sessions s WHERE s.session_id = ar.session_id
  )
ON CONFLICT (session_id) DO NOTHING;

-- 2. Backfill missing sessions that only have chunk_analysis_results rows.
INSERT INTO sessions (
    session_id,
    profile_id,
    status,
    idempotency_key,
    wod_description,
    workout_type,
    movement_hints,
    wod_evaluation_version,
    created_at,
    updated_at
)
SELECT
    c.session_id,
    (ARRAY_AGG(c.profile_id ORDER BY c.id ASC))[1],
    'started',
    'backfill:' || c.session_id,
    '',
    CASE
        WHEN c.session_id ~* '^(P[0-9]+-)?WARMUP-' THEN 'warmup'
        WHEN c.session_id ~* '^(P[0-9]+-)?ACCESSORY-' THEN 'accessory'
        WHEN c.session_id ~* '^(P[0-9]+-)?COOLDOWN-' THEN 'cooldown'
        ELSE 'wod'
    END,
    '[]'::jsonb,
    0,
    COALESCE(MIN(c.created_at), NOW()),
    COALESCE(MAX(c.updated_at), MIN(c.created_at), NOW())
FROM chunk_analysis_results c
WHERE TRIM(c.session_id) <> ''
  AND NOT EXISTS (
      SELECT 1 FROM sessions s WHERE s.session_id = c.session_id
  )
GROUP BY c.session_id
ON CONFLICT (session_id) DO NOTHING;

-- 3. Backfill missing sessions that only have recording_media_runs rows.
INSERT INTO sessions (
    session_id,
    profile_id,
    status,
    idempotency_key,
    wod_description,
    workout_type,
    movement_hints,
    wod_evaluation_version,
    created_at,
    updated_at
)
SELECT
    r.session_id,
    r.profile_id::integer,
    'started',
    'backfill:' || r.session_id,
    '',
    CASE
        WHEN r.session_id ~* '^(P[0-9]+-)?WARMUP-' THEN 'warmup'
        WHEN r.session_id ~* '^(P[0-9]+-)?ACCESSORY-' THEN 'accessory'
        WHEN r.session_id ~* '^(P[0-9]+-)?COOLDOWN-' THEN 'cooldown'
        ELSE 'wod'
    END,
    '[]'::jsonb,
    0,
    COALESCE(r.created_at, NOW()),
    COALESCE(r.updated_at, r.created_at, NOW())
FROM recording_media_runs r
WHERE TRIM(r.session_id) <> ''
  AND NOT EXISTS (
      SELECT 1 FROM sessions s WHERE s.session_id = r.session_id
  )
ON CONFLICT (session_id) DO NOTHING;

-- 4. Disambiguate any pre-existing duplicate non-empty (profile_id, idempotency_key) pairs
-- before creating the partial unique index.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (PARTITION BY profile_id, idempotency_key ORDER BY id ASC) AS rn
    FROM sessions
    WHERE TRIM(idempotency_key) <> ''
)
UPDATE sessions s
SET idempotency_key = s.idempotency_key || ':dup:' || s.id
FROM ranked r
WHERE s.id = r.id
  AND r.rn > 1;

-- 5. Enforce race-free idempotency for non-empty idempotency keys per profile.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_profile_idempotency_key
ON sessions (profile_id, idempotency_key)
WHERE TRIM(idempotency_key) <> '';
