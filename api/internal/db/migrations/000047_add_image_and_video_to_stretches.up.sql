ALTER TABLE stretches ADD COLUMN image_object TEXT NOT NULL DEFAULT '';
ALTER TABLE stretches ADD COLUMN video_object TEXT NOT NULL DEFAULT '';

UPDATE stretches SET image_object = media_object WHERE media_type = 'image';
UPDATE stretches SET video_object = media_object WHERE media_type = 'video';

ALTER TABLE stretches DROP COLUMN IF EXISTS media_type;
ALTER TABLE stretches DROP COLUMN IF EXISTS media_object;
