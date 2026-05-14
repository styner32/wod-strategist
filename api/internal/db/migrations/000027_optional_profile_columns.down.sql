-- Revert: make profile columns NOT NULL again with defaults.
ALTER TABLE profiles
  ALTER COLUMN birth_year SET NOT NULL,
  ALTER COLUMN birth_month SET NOT NULL,
  ALTER COLUMN birth_day SET NOT NULL,
  ALTER COLUMN gender SET NOT NULL,
  ALTER COLUMN height_cm SET NOT NULL,
  ALTER COLUMN weight_kg SET NOT NULL,
  ALTER COLUMN injuries SET NOT NULL,
  ALTER COLUMN injuries SET DEFAULT '[]';
