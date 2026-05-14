-- 1. Drop the FK constraint on profiles.user_id
ALTER TABLE profiles DROP CONSTRAINT IF EXISTS profiles_user_id_fkey;

-- 2. Add a serial column to users
ALTER TABLE users ADD COLUMN new_id SERIAL;

-- 3. Create a mapping table old TEXT → new INT
CREATE TEMP TABLE _user_id_map AS
  SELECT id AS old_id, new_id FROM users;

-- 4. Convert profiles.user_id from TEXT to INTEGER via the mapping
ALTER TABLE profiles ADD COLUMN new_user_id INTEGER;
UPDATE profiles p SET new_user_id = m.new_id
  FROM _user_id_map m WHERE p.user_id = m.old_id;
ALTER TABLE profiles DROP COLUMN user_id;
ALTER TABLE profiles RENAME COLUMN new_user_id TO user_id;

-- 5. Swap users PK: drop old TEXT id, promote new_id
ALTER TABLE users DROP CONSTRAINT users_pkey;
ALTER TABLE users DROP COLUMN id;
ALTER TABLE users RENAME COLUMN new_id TO id;
ALTER TABLE users ADD PRIMARY KEY (id);

-- 6. Re-establish FK + index on profiles.user_id
ALTER TABLE profiles
  ADD CONSTRAINT profiles_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_profiles_user_id ON profiles(user_id);

-- 7. Make profiles.user_id NOT NULL (was enforced by migration 24)
ALTER TABLE profiles ALTER COLUMN user_id SET NOT NULL;

-- 8. Clean up
DROP TABLE IF EXISTS _user_id_map;
