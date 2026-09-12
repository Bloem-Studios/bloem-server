-- +goose NO TRANSACTION
-- +goose Up
-- Batched subtitle-language repair. Each batch commits independently so a
-- large library does not hold one startup transaction for the full rewrite.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.canonical_subtitle_language_tag(value text)
RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT CASE lower(btrim(value))
    WHEN 'eng' THEN 'en' WHEN 'ara' THEN 'ar' WHEN 'arabic' THEN 'ar'
    WHEN 'english' THEN 'en' WHEN 'pt-br' THEN 'pt-BR'
    WHEN 'zh-hant' THEN 'zh-Hant' WHEN 'chinese (traditional)' THEN 'zh-Hant'
    WHEN 'por' THEN 'pt' WHEN 'zho' THEN 'zh'
    ELSE CASE WHEN lower(btrim(value)) ~ '^[a-z]{2,3}$' THEN lower(btrim(value)) ELSE '' END
  END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE public.silo_backfill_subtitle_language_tags()
LANGUAGE plpgsql AS $$
DECLARE last_id bigint := 0; batch_max bigint; changed bigint; batch_candidates bigint; batches bigint := 0; total bigint; done bigint := 0;
BEGIN
  SELECT count(*) INTO total FROM public.media_files mf WHERE (mf.subtitle_tracks IS NOT NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.subtitle_tracks)='array' THEN mf.subtitle_tracks ELSE '[]'::jsonb END) t WHERE t ? 'language' AND public.canonical_subtitle_language_tag(t->>'language') IS DISTINCT FROM t->>'language')) OR (mf.external_subtitles IS NOT NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.external_subtitles)='array' THEN mf.external_subtitles ELSE '[]'::jsonb END) t WHERE t ? 'language' AND public.canonical_subtitle_language_tag(t->>'language') IS DISTINCT FROM t->>'language'));
  RAISE NOTICE 'subtitle language repair starting: % candidate media_files', total;
  LOOP
    SELECT max(id) INTO batch_max FROM (SELECT id FROM public.media_files WHERE id > last_id ORDER BY id LIMIT 20000) q;
    EXIT WHEN batch_max IS NULL;
    SELECT count(*) INTO batch_candidates FROM public.media_files mf WHERE mf.id > last_id AND mf.id <= batch_max AND ((mf.subtitle_tracks IS NOT NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.subtitle_tracks)='array' THEN mf.subtitle_tracks ELSE '[]'::jsonb END) t WHERE t ? 'language' AND public.canonical_subtitle_language_tag(t->>'language') IS DISTINCT FROM t->>'language')) OR (mf.external_subtitles IS NOT NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.external_subtitles)='array' THEN mf.external_subtitles ELSE '[]'::jsonb END) t WHERE t ? 'language' AND public.canonical_subtitle_language_tag(t->>'language') IS DISTINCT FROM t->>'language')));
    UPDATE public.media_files mf SET subtitle_tracks = (SELECT jsonb_agg(CASE WHEN elem ? 'language' THEN jsonb_set(elem,'{language}',to_jsonb(public.canonical_subtitle_language_tag(elem->>'language'))) ELSE elem END ORDER BY ord) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.subtitle_tracks)='array' THEN mf.subtitle_tracks ELSE '[]'::jsonb END) WITH ORDINALITY x(elem,ord)) WHERE mf.id > last_id AND mf.id <= batch_max AND mf.subtitle_tracks IS NOT NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.subtitle_tracks)='array' THEN mf.subtitle_tracks ELSE '[]'::jsonb END) t WHERE t ? 'language' AND public.canonical_subtitle_language_tag(t->>'language') IS DISTINCT FROM t->>'language');
    GET DIAGNOSTICS changed = ROW_COUNT;
    UPDATE public.media_files mf SET external_subtitles = (SELECT jsonb_agg(CASE WHEN elem ? 'language' THEN jsonb_set(elem,'{language}',to_jsonb(public.canonical_subtitle_language_tag(elem->>'language'))) ELSE elem END ORDER BY ord) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.external_subtitles)='array' THEN mf.external_subtitles ELSE '[]'::jsonb END) WITH ORDINALITY x(elem,ord)) WHERE mf.id > last_id AND mf.id <= batch_max AND mf.external_subtitles IS NOT NULL AND EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mf.external_subtitles)='array' THEN mf.external_subtitles ELSE '[]'::jsonb END) t WHERE t ? 'language' AND public.canonical_subtitle_language_tag(t->>'language') IS DISTINCT FROM t->>'language');
    GET DIAGNOSTICS changed = ROW_COUNT;
    done := done + batch_candidates; batches := batches + 1; RAISE NOTICE 'subtitle language repair batch %, through media_file id %, rows touched %, candidates done %, progress %%%', batches, batch_max, changed, done, round(100.0 * done / NULLIF(total, 0), 1);
    COMMIT; last_id := batch_max;
  END LOOP;
END $$;
-- +goose StatementEnd
CALL public.silo_backfill_subtitle_language_tags();
DROP PROCEDURE public.silo_backfill_subtitle_language_tags();
DROP FUNCTION public.canonical_subtitle_language_tag(text);

-- +goose Down
SELECT 1;
