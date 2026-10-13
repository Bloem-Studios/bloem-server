-- +goose Up
CREATE SEQUENCE bloem_companion_book_revision;
CREATE TABLE bloem_companion_book_clock (
 user_id integer NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 profile_id text NOT NULL,
 content_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN('ebook','audiobook')),
 revision bigint NOT NULL DEFAULT nextval('bloem_companion_book_revision'),
 file_id bigint,
 reset boolean NOT NULL,
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(user_id,profile_id,content_id)
);
-- +goose StatementBegin
CREATE FUNCTION bloem_companion_record_book_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r record; item text; item_kind text; file bigint;
BEGIN
 IF TG_OP='DELETE' THEN r:=OLD; ELSE r:=NEW; END IF;
 IF TG_TABLE_NAME='ebook_reader_progress' THEN
  item:=r.content_id;item_kind:='ebook';file:=r.file_id;
 ELSE
  item:=r.media_item_id;
  SELECT type INTO item_kind FROM media_items WHERE content_id=item AND type='audiobook';
  IF item_kind IS NULL THEN IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF; END IF;
 END IF;
 INSERT INTO bloem_companion_book_clock(user_id,profile_id,content_id,kind,file_id,reset)
 VALUES(r.user_id,r.profile_id,item,item_kind,file,TG_OP='DELETE')
 ON CONFLICT(user_id,profile_id,content_id) DO UPDATE SET
 revision=nextval('bloem_companion_book_revision'),kind=EXCLUDED.kind,
 file_id=EXCLUDED.file_id,reset=EXCLUDED.reset,changed_at=clock_timestamp();
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;
-- +goose StatementEnd
CREATE TRIGGER bloem_companion_ebook_changes AFTER INSERT OR UPDATE OR DELETE ON ebook_reader_progress FOR EACH ROW EXECUTE FUNCTION bloem_companion_record_book_change();
CREATE TRIGGER bloem_companion_audiobook_changes AFTER INSERT OR UPDATE OR DELETE ON user_watch_progress FOR EACH ROW EXECUTE FUNCTION bloem_companion_record_book_change();
INSERT INTO bloem_companion_book_clock(user_id,profile_id,content_id,kind,file_id,reset,changed_at)
 SELECT user_id,profile_id,content_id,'ebook',file_id,false,updated_at FROM ebook_reader_progress;
INSERT INTO bloem_companion_book_clock(user_id,profile_id,content_id,kind,reset,changed_at)
 SELECT p.user_id,p.profile_id,p.media_item_id,'audiobook',false,p.updated_at FROM user_watch_progress p JOIN media_items m ON m.content_id=p.media_item_id AND m.type='audiobook'
 ON CONFLICT(user_id,profile_id,content_id) DO NOTHING;
-- +goose Down
DROP TRIGGER bloem_companion_audiobook_changes ON user_watch_progress;
DROP TRIGGER bloem_companion_ebook_changes ON ebook_reader_progress;
DROP FUNCTION bloem_companion_record_book_change();
DROP TABLE bloem_companion_book_clock;
DROP SEQUENCE bloem_companion_book_revision;
