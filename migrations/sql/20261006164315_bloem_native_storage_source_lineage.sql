-- +goose Up
-- Quiesce the existing installation -> marker -> source write order before
-- deciding whether any attached marked association can be retained reliably.
LOCK TABLE public.plugin_installations,public.bloem_storage_installations,public.bloem_storage_sources IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(
  SELECT 1 FROM public.bloem_storage_sources s
  JOIN public.bloem_storage_installations n ON n.installation_id=s.installation_id
  LEFT JOIN public.plugin_installations i ON i.id=s.installation_id
  WHERE i.id IS NULL OR i.kind<>'plugin' OR i.plugin_id<>s.plugin_id
   OR i.owner_id<>s.owner_id OR n.owner_id<>s.owner_id
   OR n.protocol_version<>1 OR i.runtime_generation<=0
 ) THEN
  RAISE EXCEPTION 'invalid marked native source association: refusing lineage backfill';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE public.bloem_storage_sources ADD COLUMN latest_installation_id bigint,
 ADD CONSTRAINT bloem_storage_sources_latest_installation_positive
 CHECK(latest_installation_id IS NULL OR latest_installation_id>0);
-- Only a current verified marked attachment is evidence. Unknown detached and
-- unmarked legacy sources remain NULL; identity similarities are not lineage.
UPDATE public.bloem_storage_sources s SET latest_installation_id=s.installation_id
 FROM public.bloem_storage_installations n,public.plugin_installations i
 WHERE n.installation_id=s.installation_id AND i.id=s.installation_id
 AND n.owner_id=s.owner_id AND i.owner_id=s.owner_id AND i.plugin_id=s.plugin_id
 AND i.kind='plugin' AND n.protocol_version=1 AND i.runtime_generation>0;

-- +goose Down
LOCK TABLE public.bloem_storage_sources IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM public.bloem_storage_sources) THEN
  RAISE EXCEPTION 'native source lineage retained: refusing destructive rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE public.bloem_storage_sources DROP CONSTRAINT bloem_storage_sources_latest_installation_positive,
 DROP COLUMN latest_installation_id;
