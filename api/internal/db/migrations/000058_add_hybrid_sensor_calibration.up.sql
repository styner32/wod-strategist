ALTER TABLE profiles ADD COLUMN resting_hr_bpm SMALLINT DEFAULT NULL CHECK (resting_hr_bpm BETWEEN 30 AND 120);
ALTER TABLE profiles ADD COLUMN sensor_enmo_rest_g DOUBLE PRECISION DEFAULT NULL CHECK (sensor_enmo_rest_g >= 0 AND sensor_enmo_rest_g < 1);
ALTER TABLE profiles ADD COLUMN sensor_enmo_ref_g DOUBLE PRECISION DEFAULT NULL CHECK (sensor_enmo_ref_g > 0 AND sensor_enmo_ref_g <= 5);
ALTER TABLE profiles ADD COLUMN sensor_calibration_version TEXT DEFAULT NULL;
ALTER TABLE profiles ADD COLUMN sensor_calibrated_at TIMESTAMPTZ DEFAULT NULL;
ALTER TABLE profiles ADD COLUMN sensor_calibration_sessions INTEGER NOT NULL DEFAULT 0 CHECK (sensor_calibration_sessions >= 0);
