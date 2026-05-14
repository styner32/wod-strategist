ALTER TABLE profiles ADD COLUMN user_id TEXT REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX idx_profiles_user_id ON profiles(user_id);
