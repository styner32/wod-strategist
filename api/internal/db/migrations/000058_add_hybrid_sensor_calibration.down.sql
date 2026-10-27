ALTER TABLE profiles DROP COLUMN IF EXISTS sensor_calibration_sessions;
ALTER TABLE profiles DROP COLUMN IF EXISTS sensor_calibrated_at;
ALTER TABLE profiles DROP COLUMN IF EXISTS sensor_calibration_version;
ALTER TABLE profiles DROP COLUMN IF EXISTS sensor_enmo_ref_g;
ALTER TABLE profiles DROP COLUMN IF EXISTS sensor_enmo_rest_g;
ALTER TABLE profiles DROP COLUMN IF EXISTS resting_hr_bpm;
