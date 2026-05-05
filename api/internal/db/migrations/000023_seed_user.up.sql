-- Seed a default user to own all existing profiles.
-- Password: "changeme123" (bcrypt hash)
-- User ID: "seed-user-01"
INSERT INTO users (id, username, password_hash, token_version)
VALUES (
    'seed-user-01',
    'admin',
    '$2a$10$mDhSU9B7Gi46SB8HTC5lFeyUdVVXH57Q4sS5kuwQAQ0j9YJ9gXqqy',
    1
) ON CONFLICT DO NOTHING;

-- Assign all orphan profiles to the seed user
UPDATE profiles SET user_id = 'seed-user-01' WHERE user_id IS NULL;
