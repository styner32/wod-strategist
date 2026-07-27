DROP TABLE IF EXISTS session_appearance_hints;
ALTER TABLE profiles DROP CONSTRAINT IF EXISTS profiles_appearance_object_check;
ALTER TABLE profiles DROP COLUMN IF EXISTS appearance;
