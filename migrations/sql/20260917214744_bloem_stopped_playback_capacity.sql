-- +goose Up
-- +goose StatementBegin
-- A stop committed on any API replica releases Bloem's capacity atomically.
-- Admission takes this same account lock before checking stopped_at, so an
-- in-flight acquire either precedes this deletion or sees the stopped attempt.
CREATE FUNCTION bloem_release_stopped_playback_capacity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('playback-account:' || NEW.user_id::text, 0));
    DELETE FROM playback_capacity_reservations WHERE session_id = NEW.session_id::text;
    RETURN NEW;
END;
$$;
CREATE TRIGGER bloem_stopped_playback_capacity
AFTER UPDATE OF stopped_at ON playback_v3_attempts
FOR EACH ROW WHEN (OLD.stopped_at IS NULL AND NEW.stopped_at IS NOT NULL)
EXECUTE FUNCTION bloem_release_stopped_playback_capacity();
DELETE FROM playback_capacity_reservations r USING playback_v3_attempts a
WHERE a.session_id::text = r.session_id AND a.stopped_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS bloem_stopped_playback_capacity ON playback_v3_attempts;
DROP FUNCTION IF EXISTS bloem_release_stopped_playback_capacity();
