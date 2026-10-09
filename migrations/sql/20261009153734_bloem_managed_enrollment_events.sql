-- +goose Up
-- +goose StatementBegin
ALTER TABLE bloem_managed_grants ADD COLUMN renewed_at timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE TABLE bloem_managed_enrollments(
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 account_id integer NOT NULL,profile_id text NOT NULL,enrollment_id text NOT NULL,
 generation bigint NOT NULL,destination_account_id text NOT NULL,destination_profile_id text NOT NULL,
 payload_digest bytea NOT NULL,active boolean NOT NULL,
 PRIMARY KEY(installation_id,account_id,profile_id),UNIQUE(installation_id,enrollment_id));
CREATE TABLE bloem_managed_events(
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 account_id integer NOT NULL,profile_id text NOT NULL,event_id text NOT NULL,
 entity_id text NOT NULL,revision bigint NOT NULL,consumption_id text NOT NULL DEFAULT '',digest bytea NOT NULL,
 PRIMARY KEY(installation_id,account_id,profile_id,event_id));
CREATE TABLE bloem_managed_playback_sources(
 revision bigint PRIMARY KEY,user_id integer NOT NULL,profile_id text NOT NULL,media_item_id text NOT NULL,
 history_id text NOT NULL DEFAULT '',session_id text NOT NULL DEFAULT '',action text NOT NULL,
 progress double precision NOT NULL DEFAULT 0,duration double precision NOT NULL DEFAULT 0,completed boolean NOT NULL);
CREATE INDEX bloem_managed_sources_history ON bloem_managed_playback_sources(user_id,profile_id,history_id);
CREATE INDEX bloem_managed_sources_session ON bloem_managed_playback_sources(user_id,profile_id,session_id,action);
CREATE FUNCTION bloem_managed_playback_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE rev bigint; account integer; profile text;
BEGIN
 IF TG_TABLE_NAME='user_watch_history' THEN
  UPDATE bloem_managed_clock SET revision=revision+1 WHERE singleton RETURNING revision INTO rev;
  INSERT INTO bloem_managed_playback_sources(revision,user_id,profile_id,media_item_id,history_id,action,duration,completed)
  VALUES(rev,NEW.user_id,NEW.profile_id,NEW.media_item_id,NEW.id,'history',coalesce(NEW.duration_seconds,0),NEW.completed);
 ELSE
  SELECT user_id,profile_id INTO account,profile FROM watch_provider_connections WHERE id=NEW.connection_id AND provider LIKE 'plugin:%:pastime';
  IF account IS NOT NULL THEN
   UPDATE bloem_managed_clock SET revision=revision+1 WHERE singleton RETURNING revision INTO rev;
   INSERT INTO bloem_managed_playback_sources(revision,user_id,profile_id,media_item_id,history_id,session_id,action,progress,duration,completed)
   VALUES(rev,account,profile,NEW.media_item_id,NEW.history_id,NEW.playback_session_id,NEW.last_action,NEW.last_progress,NEW.duration_seconds,NEW.completed);
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER bloem_managed_history AFTER INSERT OR UPDATE OF completed,duration_seconds ON user_watch_history FOR EACH ROW EXECUTE FUNCTION bloem_managed_playback_changed();
CREATE TRIGGER bloem_managed_scrobbles AFTER INSERT OR UPDATE OF last_action,last_progress,history_id,completed ON watch_provider_scrobble_sessions FOR EACH ROW EXECUTE FUNCTION bloem_managed_playback_changed();
-- Existing history receives revisions before any new live publication.
DO $$ DECLARE h record; rev bigint; BEGIN
 FOR h IN SELECT * FROM user_watch_history ORDER BY id LOOP
  UPDATE bloem_managed_clock SET revision=revision+1 WHERE singleton RETURNING revision INTO rev;
  INSERT INTO bloem_managed_playback_sources(revision,user_id,profile_id,media_item_id,history_id,action,duration,completed)
  VALUES(rev,h.user_id,h.profile_id,h.media_item_id,h.id,'history',coalesce(h.duration_seconds,0),h.completed);
 END LOOP;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TRIGGER bloem_managed_scrobbles ON watch_provider_scrobble_sessions;
DROP TRIGGER bloem_managed_history ON user_watch_history;
DROP FUNCTION bloem_managed_playback_changed();
DROP TABLE bloem_managed_playback_sources,bloem_managed_events,bloem_managed_enrollments;
ALTER TABLE bloem_managed_grants DROP COLUMN renewed_at;
-- +goose StatementEnd
