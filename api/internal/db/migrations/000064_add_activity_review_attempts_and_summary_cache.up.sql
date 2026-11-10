ALTER TABLE sessions
    ADD COLUMN activity_review_attempts JSONB DEFAULT NULL,
    ADD COLUMN activity_summary_cache_version INTEGER NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION invalidate_session_activity_summary_cache()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE sessions
        SET activity_summary_cache_version = 0
        WHERE profile_id = NEW.profile_id AND session_id = NEW.session_id;
        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE sessions
        SET activity_summary_cache_version = 0
        WHERE profile_id = OLD.profile_id AND session_id = OLD.session_id;
        RETURN OLD;
    ELSIF TG_OP = 'UPDATE' THEN
        IF OLD.profile_id IS DISTINCT FROM NEW.profile_id
           OR OLD.session_id IS DISTINCT FROM NEW.session_id THEN
            UPDATE sessions
            SET activity_summary_cache_version = 0
            WHERE (profile_id = OLD.profile_id AND session_id = OLD.session_id)
               OR (profile_id = NEW.profile_id AND session_id = NEW.session_id);
        ELSE
            UPDATE sessions
            SET activity_summary_cache_version = 0
            WHERE profile_id = NEW.profile_id AND session_id = NEW.session_id;
        END IF;
        RETURN NEW;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_chunk_analysis_invalidate_summary_insert_delete
AFTER INSERT OR DELETE ON chunk_analysis_results
FOR EACH ROW
EXECUTE FUNCTION invalidate_session_activity_summary_cache();

CREATE TRIGGER trg_chunk_analysis_invalidate_summary_update
AFTER UPDATE OF session_id, profile_id, file_path, exercise_type, status,
                start_secs, end_secs, media_start_secs, media_end_secs, movement_observations
ON chunk_analysis_results
FOR EACH ROW
WHEN (
    OLD.session_id IS DISTINCT FROM NEW.session_id OR
    OLD.profile_id IS DISTINCT FROM NEW.profile_id OR
    OLD.file_path IS DISTINCT FROM NEW.file_path OR
    OLD.exercise_type IS DISTINCT FROM NEW.exercise_type OR
    OLD.status IS DISTINCT FROM NEW.status OR
    OLD.start_secs IS DISTINCT FROM NEW.start_secs OR
    OLD.end_secs IS DISTINCT FROM NEW.end_secs OR
    OLD.media_start_secs IS DISTINCT FROM NEW.media_start_secs OR
    OLD.media_end_secs IS DISTINCT FROM NEW.media_end_secs OR
    OLD.movement_observations IS DISTINCT FROM NEW.movement_observations
)
EXECUTE FUNCTION invalidate_session_activity_summary_cache();
