-- Remove the seed user assignment (set user_id back to NULL for profiles that had it)
UPDATE profiles SET user_id = NULL WHERE user_id = 'seed-user-01';

-- Delete the seed user
DELETE FROM users WHERE id = 'seed-user-01';
