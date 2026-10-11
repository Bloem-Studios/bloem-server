-- +goose Up
-- +goose StatementBegin
-- min_generation is the oldest credential generation that still authorizes
-- events. Periodic renewal raises generation only, so current credentials keep
-- working until their replacements are enrolled; rotation and revocation raise
-- both and cut every older credential at once.
ALTER TABLE bloem_managed_grants ADD COLUMN renewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 ADD COLUMN min_generation bigint NOT NULL DEFAULT 1 CHECK(min_generation>0);
CREATE TABLE bloem_managed_enrollments(
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 account_id integer NOT NULL,profile_id text NOT NULL,enrollment_id text NOT NULL,
 generation bigint NOT NULL,destination_account_id text NOT NULL,destination_profile_id text NOT NULL,
 payload_digest bytea NOT NULL,active boolean NOT NULL,
 PRIMARY KEY(installation_id,account_id,profile_id),UNIQUE(installation_id,enrollment_id));
-- Receipts live only as long as a plugin may still resolve them during one
-- ApplyEvents call or its retries; GetInfo prunes old ones.
CREATE TABLE bloem_managed_events(
 installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 account_id integer NOT NULL,profile_id text NOT NULL,event_id text NOT NULL,
 entity_id text NOT NULL,revision bigint NOT NULL,consumption_id text NOT NULL DEFAULT '',digest bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(installation_id,account_id,profile_id,event_id));
CREATE INDEX bloem_managed_events_age ON bloem_managed_events(installation_id,created_at);
-- Playback source revisions order history and live playback per title. A
-- sequence hands them out without a shared row lock, so watch-history writes
-- never serialize on each other.
CREATE SEQUENCE bloem_managed_playback_revision;
CREATE TABLE bloem_managed_playback_sources(
 revision bigint PRIMARY KEY DEFAULT nextval('bloem_managed_playback_revision'),
 user_id integer NOT NULL,profile_id text NOT NULL,media_item_id text NOT NULL,
 history_id text NOT NULL DEFAULT '',session_id text NOT NULL DEFAULT '',action text NOT NULL,
 progress double precision NOT NULL DEFAULT 0,duration double precision NOT NULL DEFAULT 0,completed boolean NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp());
ALTER SEQUENCE bloem_managed_playback_revision OWNED BY bloem_managed_playback_sources.revision;
CREATE INDEX bloem_managed_sources_history ON bloem_managed_playback_sources(history_id,media_item_id) WHERE action='history';
CREATE INDEX bloem_managed_sources_session ON bloem_managed_playback_sources(user_id,profile_id,session_id,action);
-- Only profiles of a tenant with a managed grant record history sources; the
-- grant backfills its tenant's existing history when it is created.
CREATE FUNCTION bloem_managed_history_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM user_profiles p JOIN bloem_managed_grants g ON g.tenant_id=p.organization_id WHERE p.id=NEW.profile_id) THEN
  INSERT INTO bloem_managed_playback_sources(user_id,profile_id,media_item_id,history_id,action,duration,completed)
  VALUES(NEW.user_id,NEW.profile_id,NEW.media_item_id,NEW.id,'history',coalesce(NEW.duration_seconds,0),true);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER bloem_managed_history_insert AFTER INSERT ON user_watch_history FOR EACH ROW
 WHEN (NEW.completed) EXECUTE FUNCTION bloem_managed_history_changed();
-- A completion, a move to another media item, or a move to another profile
-- starts a new source; unrelated updates do not.
CREATE TRIGGER bloem_managed_history_update AFTER UPDATE OF completed,media_item_id,user_id,profile_id ON user_watch_history FOR EACH ROW
 WHEN (NEW.completed AND (OLD.completed IS DISTINCT FROM NEW.completed OR OLD.media_item_id IS DISTINCT FROM NEW.media_item_id
  OR OLD.user_id IS DISTINCT FROM NEW.user_id OR OLD.profile_id IS DISTINCT FROM NEW.profile_id))
 EXECUTE FUNCTION bloem_managed_history_changed();
-- Live playback sources come only from connections of granted bloem.pastime
-- installations, matched by their exact provider key.
CREATE FUNCTION bloem_managed_scrobble_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account integer; profile text;
BEGIN
 SELECT c.user_id,c.profile_id INTO account,profile FROM watch_provider_connections c
 WHERE c.id=NEW.connection_id AND c.provider LIKE 'plugin:%:pastime'
  AND EXISTS(SELECT 1 FROM bloem_managed_grants g WHERE c.provider='plugin:'||g.installation_id||':pastime');
 IF account IS NOT NULL THEN
  INSERT INTO bloem_managed_playback_sources(user_id,profile_id,media_item_id,history_id,session_id,action,progress,duration,completed)
  VALUES(account,profile,NEW.media_item_id,NEW.history_id,NEW.playback_session_id,NEW.last_action,NEW.last_progress,NEW.duration_seconds,NEW.completed);
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER bloem_managed_scrobbles_insert AFTER INSERT ON watch_provider_scrobble_sessions FOR EACH ROW
 EXECUTE FUNCTION bloem_managed_scrobble_changed();
CREATE TRIGGER bloem_managed_scrobbles_update AFTER UPDATE OF last_action,last_progress,history_id,completed,media_item_id ON watch_provider_scrobble_sessions FOR EACH ROW
 WHEN (OLD.last_action IS DISTINCT FROM NEW.last_action OR OLD.last_progress IS DISTINCT FROM NEW.last_progress
  OR OLD.history_id IS DISTINCT FROM NEW.history_id OR OLD.completed IS DISTINCT FROM NEW.completed
  OR OLD.media_item_id IS DISTINCT FROM NEW.media_item_id)
 EXECUTE FUNCTION bloem_managed_scrobble_changed();
-- Managed credentials have one writer. Generic watch-provider upserts built
-- from an older in-memory connection keep the stored credential instead of
-- restoring a revoked one; every other column still updates.
CREATE FUNCTION bloem_managed_connection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF current_setting('bloem.managed_tracking_writer',true) IS DISTINCT FROM 'v1'
  AND EXISTS(SELECT 1 FROM bloem_managed_grants g WHERE OLD.provider='plugin:'||g.installation_id||':pastime') THEN
  NEW.provider_account_id:=OLD.provider_account_id;
  NEW.provider_username:=OLD.provider_username;
  NEW.access_token:=OLD.access_token;
  NEW.refresh_token:=OLD.refresh_token;
  NEW.token_expires_at:=OLD.token_expires_at;
  NEW.plugin_credentials:=OLD.plugin_credentials;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER bloem_managed_connection_guard BEFORE UPDATE ON watch_provider_connections FOR EACH ROW
 WHEN (OLD.provider LIKE 'plugin:%:pastime') EXECUTE FUNCTION bloem_managed_connection_guard();
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TRIGGER bloem_managed_connection_guard ON watch_provider_connections;
DROP FUNCTION bloem_managed_connection_guard();
DROP TRIGGER bloem_managed_scrobbles_update ON watch_provider_scrobble_sessions;
DROP TRIGGER bloem_managed_scrobbles_insert ON watch_provider_scrobble_sessions;
DROP FUNCTION bloem_managed_scrobble_changed();
DROP TRIGGER bloem_managed_history_update ON user_watch_history;
DROP TRIGGER bloem_managed_history_insert ON user_watch_history;
DROP FUNCTION bloem_managed_history_changed();
DROP TABLE bloem_managed_playback_sources,bloem_managed_events,bloem_managed_enrollments;
ALTER TABLE bloem_managed_grants DROP COLUMN min_generation,DROP COLUMN renewed_at;
-- +goose StatementEnd
