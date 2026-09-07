ALTER TABLE stretches ADD COLUMN media_type TEXT NOT NULL DEFAULT 'none';
ALTER TABLE stretches ADD COLUMN media_object TEXT NOT NULL DEFAULT '';

UPDATE stretches SET media_type = 'image', media_object = image_object WHERE image_object <> '';
UPDATE stretches SET media_type = 'video', media_object = video_object WHERE video_object <> '' AND image_object = '';

ALTER TABLE stretches DROP COLUMN IF EXISTS image_object;
ALTER TABLE stretches DROP COLUMN IF EXISTS video_object;
