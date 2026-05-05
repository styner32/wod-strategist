-- Revert users.id from SERIAL back to TEXT (lossy: original string IDs are lost).
-- 1. Drop FK
ALTER TABLE profiles DROP CONSTRAINT IF EXISTS profiles_user_id_fkey;

-- 2. Convert users.id back to TEXT
ALTER TABLE users DROP CONSTRAINT users_pkey;
ALTER TABLE users ADD COLUMN old_id TEXT;
UPDATE users SET old_id = id::TEXT;
ALTER TABLE users DROP COLUMN id;
ALTER TABLE users RENAME COLUMN old_id TO id;
ALTER TABLE users ADD PRIMARY KEY (id);

-- 3. Convert profiles.user_id back to TEXT
ALTER TABLE profiles ADD COLUMN old_user_id TEXT;
UPDATE profiles SET old_user_id = user_id::TEXT;
ALTER TABLE profiles DROP COLUMN user_id;
ALTER TABLE profiles RENAME COLUMN old_user_id TO user_id;
ALTER TABLE profiles ALTER COLUMN user_id SET NOT NULL;

-- 4. Re-establish FK + index
ALTER TABLE profiles
  ADD CONSTRAINT profiles_user_id_fkey
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_profiles_user_id ON profiles(user_id);
