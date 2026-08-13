CREATE TABLE stretches (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    normalized_key TEXT NOT NULL UNIQUE CHECK (char_length(normalized_key) > 0),
    target_area TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 4000),
    duration_hint TEXT NOT NULL DEFAULT '' CHECK (char_length(duration_hint) <= 200),
    caution TEXT NOT NULL DEFAULT '' CHECK (char_length(caution) <= 1000),
    media_type TEXT NOT NULL DEFAULT 'none' CHECK (media_type IN ('none','image','video')),
    media_object TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE stretch_aliases (
    id BIGSERIAL PRIMARY KEY,
    stretch_id BIGINT NOT NULL REFERENCES stretches(id) ON DELETE CASCADE,
    alias TEXT NOT NULL CHECK (char_length(alias) BETWEEN 1 AND 120),
    normalized_key TEXT NOT NULL UNIQUE CHECK (char_length(normalized_key) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stretch_aliases_stretch_id ON stretch_aliases (stretch_id);

INSERT INTO stretches (name, normalized_key, target_area, description, duration_hint, caution) VALUES
('Pigeon Pose', 'pigeon pose', 'Hips & Glutes', 'Relieves deep hip rotators and glute tightness after heavy squatting or jumping.', '90s per side', 'Keep front knee protected — bend knee to 90 degrees if you feel joint pressure.'),
('Couch Stretch', 'couch stretch', 'Hips & Glutes', 'Opens tight hip flexors and quadriceps to improve squat depth and lower back position.', '2 mins per side', 'Avoid excessive arching in the lower back; squeeze glutes to protect spine.'),
('Samson (Hip Flexor Lunge) Stretch', 'samson (hip flexor lunge) stretch', 'Hips & Glutes', 'Elongates the hip flexors, abdominal wall, and lats in an overhead lunging position.', '60s per side', 'Maintain an upright torso and do not overarch the low back.'),
('Cossack Squat Hold', 'cossack squat hold', 'Hips & Glutes', 'Improves lateral hip mobility, groin openness, and ankle dorsiflexion.', '45s per side', 'Keep the planted heel flat on the floor and chest lifted.'),
('Ankle Dorsiflexion Rock', 'ankle dorsiflexion rock', 'Legs & Ankles', 'Enhances ankle mobility for deeper squat position without heel lifting.', '15-20 reps per ankle', 'Keep heel firmly planted on ground throughout movement.'),
('Calf Stretch', 'calf stretch', 'Legs & Ankles', 'Stretches the gastrocnemius and soleus to alleviate Achilles tendon strain.', '60s per leg', 'Keep rear leg straight and back heel pressed flat down.'),
('Standing Forward Fold', 'standing forward fold', 'Legs & Ankles', 'Decompresses the posterior chain including hamstrings, calves, and spine.', '60-90s hold', 'Bend knees slightly if hamstring tightness pulls on the lower back.'),
('Hamstring Floss', 'hamstring floss', 'Legs & Ankles', 'Dynamically mobilizes the hamstring complex and sciatic nerve path.', '15 reps per leg', 'Perform smoothly without bouncing or sharp force.'),
('Child''s Pose', 'child''s pose', 'Spine & Back', 'Gently stretches the lower back, lats, and shoulders while promoting relaxation.', '60-120s hold', 'Rest hips back toward heels without forcing tight knees.'),
('Downward Dog', 'downward dog', 'Spine & Back', 'Opens the shoulders, thoracic spine, hamstrings, and calves simultaneously.', '60s hold', 'Press evenly through palms and keep neck relaxed.'),
('Thread the Needle', 'thread the needle', 'Spine & Back', 'Mobilizes thoracic rotation and relieves shoulder blade tension.', '60s per side', 'Rest side of head softly on ground without pressing neck.'),
('Cat-Cow', 'cat cow', 'Spine & Back', 'Restores segmental spinal flexion and extension mobility.', '10-12 slow fluid reps', 'Move within a comfortable pain-free range of motion.'),
('Thoracic Extension over Foam Roller', 'thoracic extension over foam roller', 'Spine & Back', 'Improves upper back extension for safer overhead position and front rack stability.', '2 mins total', 'Support neck with hands; do not roll lower back on the foam roller.'),
('Doorway Pec Stretch', 'doorway pec stretch', 'Chest & Shoulders', 'Restores anterior shoulder mobility and opens chest after overhead presses and push-ups.', '60s per arm angle', 'Do not force rotation through shoulder joint; keep core engaged.'),
('Wall Slide', 'wall slide', 'Chest & Shoulders', 'Activates lower traps and improves scapular upward rotation and shoulder health.', '12-15 controlled reps', 'Keep ribcage down and lower back flat against wall.'),
('Wrist Flexor/Extensor Stretch', 'wrist flexor/extensor stretch', 'Arms & Neck', 'Eases forearm strain and wrist compression from front rack and barbell work.', '45s each direction', 'Apply gentle pressure; avoid sharp pain in wrist joint.'),
('Neck Lateral Stretch', 'neck lateral stretch', 'Arms & Neck', 'Relieves neck stiffness and upper trapezius tension.', '30-45s per side', 'Gently guide head with hand without pulling forcefully.');

INSERT INTO stretch_aliases (stretch_id, alias, normalized_key) VALUES
((SELECT id FROM stretches WHERE normalized_key = 'thoracic extension over foam roller'), 'Thoracic Spine Foam Roll Extension', 'thoracic spine foam roll extension'),
((SELECT id FROM stretches WHERE normalized_key = 'ankle dorsiflexion rock'), 'Ankle Dorsiflexion Wall Mobilization', 'ankle dorsiflexion wall mobilization'),
((SELECT id FROM stretches WHERE normalized_key = 'samson (hip flexor lunge) stretch'), 'Samson Stretch', 'samson stretch'),
((SELECT id FROM stretches WHERE normalized_key = 'samson (hip flexor lunge) stretch'), 'Hip Flexor Stretch', 'hip flexor stretch'),
((SELECT id FROM stretches WHERE normalized_key = 'standing forward fold'), 'Forward Fold', 'forward fold');
