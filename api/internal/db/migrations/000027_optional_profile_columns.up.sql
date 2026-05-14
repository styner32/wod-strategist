-- Make profile columns optional (nullable), keep name NOT NULL.
ALTER TABLE profiles
  ALTER COLUMN name SET NOT NULL,
  ALTER COLUMN birth_year DROP NOT NULL,
  ALTER COLUMN birth_month DROP NOT NULL,
  ALTER COLUMN birth_day DROP NOT NULL,
  ALTER COLUMN gender DROP NOT NULL,
  ALTER COLUMN height_cm DROP NOT NULL,
  ALTER COLUMN weight_kg DROP NOT NULL,
  ALTER COLUMN injuries DROP NOT NULL,
  ALTER COLUMN injuries SET DEFAULT NULL;
