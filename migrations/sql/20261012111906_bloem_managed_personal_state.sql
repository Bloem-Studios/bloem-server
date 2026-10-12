-- +goose Up
-- A profile's current list/rating evidence includes removals. No upstream row
-- shape or write semantics change; revisions are ordered within each state lane.
CREATE TABLE bloem_managed_personal_states (
 user_id integer NOT NULL,
 profile_id text NOT NULL,
 media_item_id text NOT NULL,
 kind text NOT NULL CHECK (kind IN ('favorite','watchlist','rating')),
 present boolean NOT NULL,
 rating smallint NOT NULL DEFAULT 0,
 revision bigint NOT NULL CHECK (revision > 0),
 changed_at timestamptz NOT NULL,
 PRIMARY KEY (user_id,profile_id,media_item_id,kind),
 FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
-- +goose StatementBegin
CREATE FUNCTION bloem_managed_record_personal(source jsonb,lane text,is_present boolean) RETURNS void LANGUAGE plpgsql AS $$
DECLARE score smallint;
BEGIN
 -- Ignore legacy rows for missing profiles without constraining Silo writes.
 IF NOT EXISTS (SELECT 1 FROM user_profiles WHERE user_id=(source->>'user_id')::integer AND id=source->>'profile_id')
 OR NOT EXISTS (SELECT 1 FROM users WHERE id=(source->>'user_id')::integer) THEN RETURN; END IF;
 score:=CASE WHEN lane='rating' AND is_present THEN (source->>'rating')::smallint ELSE 0 END;
 INSERT INTO bloem_managed_personal_states(user_id,profile_id,media_item_id,kind,present,rating,revision,changed_at)
 VALUES ((source->>'user_id')::integer,source->>'profile_id',source->>'media_item_id',lane,is_present,score,1,clock_timestamp())
 ON CONFLICT(user_id,profile_id,media_item_id,kind) DO UPDATE SET
  present=EXCLUDED.present,rating=EXCLUDED.rating,
  revision=bloem_managed_personal_states.revision+1,changed_at=EXCLUDED.changed_at;
END $$;
CREATE FUNCTION bloem_managed_personal_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  PERFORM bloem_managed_record_personal(to_jsonb(OLD),TG_ARGV[0],false);
 ELSE
  IF TG_OP='UPDATE' THEN
   IF to_jsonb(OLD)=to_jsonb(NEW) THEN RETURN NULL; END IF;
   IF (OLD.user_id,OLD.profile_id,OLD.media_item_id) IS DISTINCT FROM (NEW.user_id,NEW.profile_id,NEW.media_item_id) THEN
    PERFORM bloem_managed_record_personal(to_jsonb(OLD),TG_ARGV[0],false);
   END IF;
  END IF;
  PERFORM bloem_managed_record_personal(to_jsonb(NEW),TG_ARGV[0],true);
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER bloem_managed_favorite_changed AFTER INSERT OR UPDATE OR DELETE ON user_favorites
 FOR EACH ROW EXECUTE FUNCTION bloem_managed_personal_changed('favorite');
CREATE TRIGGER bloem_managed_watchlist_changed AFTER INSERT OR UPDATE OR DELETE ON user_watchlist
 FOR EACH ROW EXECUTE FUNCTION bloem_managed_personal_changed('watchlist');
CREATE TRIGGER bloem_managed_rating_changed AFTER INSERT OR UPDATE OR DELETE ON user_ratings
 FOR EACH ROW EXECUTE FUNCTION bloem_managed_personal_changed('rating');
INSERT INTO bloem_managed_personal_states
 SELECT s.user_id,s.profile_id,s.media_item_id,'favorite',true,0,1,s.added_at FROM user_favorites s JOIN user_profiles p ON p.user_id=s.user_id AND p.id=s.profile_id
 UNION ALL SELECT s.user_id,s.profile_id,s.media_item_id,'watchlist',true,0,1,s.added_at FROM user_watchlist s JOIN user_profiles p ON p.user_id=s.user_id AND p.id=s.profile_id
 UNION ALL SELECT s.user_id,s.profile_id,s.media_item_id,'rating',true,s.rating,1,s.rated_at FROM user_ratings s JOIN user_profiles p ON p.user_id=s.user_id AND p.id=s.profile_id;

-- +goose Down
DROP TRIGGER bloem_managed_rating_changed ON user_ratings;
DROP TRIGGER bloem_managed_watchlist_changed ON user_watchlist;
DROP TRIGGER bloem_managed_favorite_changed ON user_favorites;
DROP FUNCTION bloem_managed_personal_changed();
DROP FUNCTION IF EXISTS bloem_managed_record_personal(jsonb,text,boolean);
DROP TABLE bloem_managed_personal_states;
