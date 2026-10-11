-- +goose Up
-- Managed tracking sends each account's verified sign-in identity (issuer and
-- subject) so Pastime can attach a person's own login to the managed account
-- that holds their tracked history. Email never links anything.
--
-- The identity comes from silo.auth.oidc links: the plugin builds
-- external_subject as iss|sub from the verified ID token, and Bloem login
-- matches on it. The installation's configured issuer is never read: its
-- runtime config is one encrypted envelope that also holds the client secret.
-- +goose StatementBegin
-- Same lock order as every publisher (identity lock, then clock, then the
-- projection), so a concurrent identity write cannot deadlock this upgrade.
DO $$ BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('bloem.managed_identity',0));
 PERFORM 1 FROM bloem_managed_clock WHERE singleton FOR UPDATE;
END $$;
ALTER TABLE bloem_managed_profiles
 ADD COLUMN identity_issuer text NOT NULL DEFAULT '',
 ADD COLUMN identity_subject text NOT NULL DEFAULT '';
-- Account refreshes, identity triggers and event authority filter by account;
-- the primary key leads with the tenant.
CREATE INDEX IF NOT EXISTS bloem_managed_profiles_account ON bloem_managed_profiles(account_id);

-- An account's identity is the one (iss, sub) its links in enabled
-- silo.auth.oidc installations agree on, split from external_subject at the
-- first '|'. An empty issuer column (links made before the column existed)
-- takes the token's issuer; a non-empty one must match it. A link the host
-- cannot interpret (an Entra tid|oid key, a malformed issuer or subject), links
-- naming different people, or a key another account also holds in an enabled
-- installation leaves the account without an identity. Returns ('','') then.
CREATE FUNCTION bloem_managed_verified_identity(target integer, OUT issuer text, OUT subject text)
LANGUAGE plpgsql STABLE AS $$
DECLARE
 control constant text := '['||chr(1)||'-'||chr(31)||chr(127)||'-'||chr(159)||']';
 entry record; cut integer; iss text; sub text; found_iss text; found_sub text;
BEGIN
 issuer := ''; subject := '';
 FOR entry IN SELECT i.external_subject AS external, i.issuer AS stored FROM plugin_auth_identities i
  JOIN plugin_installations p ON p.id=i.plugin_installation_id
  WHERE i.user_id=target AND p.plugin_id='silo.auth.oidc' AND p.enabled
 LOOP
  cut := strpos(entry.external,'|');
  IF cut=0 THEN RETURN; END IF;
  iss := left(entry.external,cut-1);
  sub := substr(entry.external,cut+1);
  IF length(iss)>500 OR iss !~ '^https://[^/?#@|[:space:]]+(/[^?#|[:space:]]*)?$' OR iss ~ control
   OR sub='' OR length(sub)>255 OR sub ~ control OR (entry.stored<>'' AND entry.stored<>iss) THEN
   RETURN;
  END IF;
  IF found_iss IS NULL THEN
   found_iss := iss; found_sub := sub;
  ELSIF found_iss<>iss OR found_sub<>sub THEN
   RETURN;
  END IF;
 END LOOP;
 IF found_iss IS NULL OR EXISTS(SELECT 1 FROM plugin_auth_identities i
  JOIN plugin_installations p ON p.id=i.plugin_installation_id
  WHERE i.external_subject=found_iss||'|'||found_sub AND i.user_id<>target
   AND p.plugin_id='silo.auth.oidc' AND p.enabled) THEN
  RETURN;
 END IF;
 issuer := found_iss; subject := found_sub;
END $$;

-- The 20261011175820 body, plus the identity lock and the identity columns.
CREATE OR REPLACE FUNCTION bloem_managed_refresh_account(target integer) RETURNS void LANGUAGE plpgsql AS $$
DECLARE rev bigint; tenant uuid; default_id text; vi_issuer text; vi_subject text;
BEGIN
 -- Identity triggers take this lock before deciding whether anything changed,
 -- so concurrent link changes see each other's commits. Every publisher takes
 -- it before the clock row, so the two locks never close a cycle.
 PERFORM pg_advisory_xact_lock(hashtextextended('bloem.managed_identity',0));
 -- One durable clock lock serializes publication with commits. Snapshots acquire
 -- this fence before copying projection and watermark, never source row locks.
 UPDATE bloem_managed_clock SET revision=revision+1 WHERE singleton RETURNING revision INTO rev;
 SELECT v.issuer,v.subject INTO vi_issuer,vi_subject FROM bloem_managed_verified_identity(target) v;
 FOR tenant IN SELECT organization_id FROM organization_memberships WHERE account_id=target
 UNION SELECT tenant_id FROM bloem_managed_profiles WHERE account_id=target
 LOOP
  SELECT default_profile_id INTO default_id FROM bloem_managed_profiles WHERE tenant_id=tenant AND account_id=target ORDER BY profile_id LIMIT 1;
  IF default_id IS NULL THEN
   SELECT id INTO default_id FROM user_profiles WHERE user_id=target AND organization_id=tenant ORDER BY is_primary DESC,id LIMIT 1;
  END IF;
  UPDATE bloem_managed_profiles SET account_active=false,active=false,account_revision=rev,revision=rev
  WHERE tenant_id=tenant AND account_id=target;
  INSERT INTO bloem_managed_profiles(tenant_id,account_id,profile_id,default_profile_id,account_name,email,name,
   account_active,active,account_revision,revision,identity_issuer,identity_subject)
  SELECT tenant,u.id,p.id,coalesce(default_id,p.id),bloem_managed_display_name(u.username,'Account'),'',bloem_managed_display_name(p.name,'Profile'),
   u.enabled AND m.status='active' AND (o.status='active' OR (o.is_default AND o.status='initializing')),true,rev,rev,vi_issuer,vi_subject
  FROM users u JOIN user_profiles p ON p.user_id=u.id
  JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=tenant
  JOIN organizations o ON o.id=tenant
  WHERE u.id=target AND p.organization_id=tenant
  ON CONFLICT(tenant_id,account_id,profile_id) DO UPDATE SET
   default_profile_id=EXCLUDED.default_profile_id,account_name=EXCLUDED.account_name,email=EXCLUDED.email,
   name=EXCLUDED.name,account_active=EXCLUDED.account_active,active=EXCLUDED.active,
   account_revision=rev,revision=rev,identity_issuer=EXCLUDED.identity_issuer,identity_subject=EXCLUDED.identity_subject;
  -- Preserve tombstones but keep account metadata/revision consistent across rows.
  UPDATE bloem_managed_profiles SET default_profile_id=coalesce(default_id,default_profile_id),
   account_name=coalesce((SELECT bloem_managed_display_name(username,'Account') FROM users WHERE id=target),account_name),
   email='',
   account_active=coalesce((SELECT u.enabled AND m.status='active' AND (o.status='active' OR (o.is_default AND o.status='initializing'))
    FROM users u JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=tenant
    JOIN organizations o ON o.id=tenant WHERE u.id=target),false),
   identity_issuer=vi_issuer,identity_subject=vi_subject
  WHERE tenant_id=tenant AND account_id=target;
  INSERT INTO bloem_managed_changes VALUES(rev,tenant,target) ON CONFLICT DO NOTHING;
 END LOOP;
END $$;

-- Publishes the account only when its derived identity differs from the
-- projection. Sign-ins and provider re-checks rewrite the issuer column of
-- existing links; those compare equal and take no clock lock and no revision.
CREATE FUNCTION bloem_managed_refresh_identity(target integer) RETURNS void LANGUAGE plpgsql AS $$
DECLARE vi_issuer text; vi_subject text;
BEGIN
 SELECT v.issuer,v.subject INTO vi_issuer,vi_subject FROM bloem_managed_verified_identity(target) v;
 IF EXISTS(SELECT 1 FROM bloem_managed_profiles WHERE account_id=target
  AND (identity_issuer,identity_subject) IS DISTINCT FROM (vi_issuer,vi_subject)) THEN
  PERFORM bloem_managed_refresh_account(target);
 END IF;
END $$;

-- A link change refreshes the accounts on both sides of it and every account
-- holding the same key in an enabled installation (the shared-key rule).
CREATE FUNCTION bloem_managed_auth_identity_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE installs bigint[]; subjects text[]; accounts integer[]; target integer;
BEGIN
 IF TG_OP='INSERT' THEN
  installs := ARRAY[NEW.plugin_installation_id]; subjects := ARRAY[NEW.external_subject]; accounts := ARRAY[NEW.user_id];
 ELSIF TG_OP='DELETE' THEN
  installs := ARRAY[OLD.plugin_installation_id]; subjects := ARRAY[OLD.external_subject]; accounts := ARRAY[OLD.user_id];
 ELSE
  installs := ARRAY[OLD.plugin_installation_id,NEW.plugin_installation_id];
  subjects := ARRAY[OLD.external_subject,NEW.external_subject];
  accounts := ARRAY[OLD.user_id,NEW.user_id];
 END IF;
 -- Other auth plugins' links never carry an identity. A link whose
 -- installation is gone (a cascading delete) still refreshes its account.
 IF NOT EXISTS(SELECT 1 FROM unnest(installs) x(id)
  WHERE NOT EXISTS(SELECT 1 FROM plugin_installations p WHERE p.id=x.id AND p.plugin_id<>'silo.auth.oidc')) THEN
  RETURN NULL;
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('bloem.managed_identity',0));
 FOR target IN SELECT a FROM unnest(accounts) a
  UNION SELECT i.user_id FROM plugin_auth_identities i JOIN plugin_installations p ON p.id=i.plugin_installation_id
   WHERE i.external_subject=ANY(subjects) AND p.plugin_id='silo.auth.oidc' AND p.enabled
  ORDER BY 1
 LOOP
  PERFORM bloem_managed_refresh_identity(target);
 END LOOP;
 RETURN NULL;
END $$;
CREATE TRIGGER bloem_managed_auth_identities AFTER INSERT OR DELETE ON plugin_auth_identities
 FOR EACH ROW EXECUTE FUNCTION bloem_managed_auth_identity_changed();
CREATE TRIGGER bloem_managed_auth_identities_update
 AFTER UPDATE OF external_subject,user_id,issuer,plugin_installation_id ON plugin_auth_identities
 FOR EACH ROW WHEN (OLD.external_subject IS DISTINCT FROM NEW.external_subject OR OLD.user_id IS DISTINCT FROM NEW.user_id
  OR OLD.issuer IS DISTINCT FROM NEW.issuer OR OLD.plugin_installation_id IS DISTINCT FROM NEW.plugin_installation_id)
 EXECUTE FUNCTION bloem_managed_auth_identity_changed();

-- Enabling, disabling or re-identifying an OIDC installation refreshes its
-- links' accounts and every account sharing one of its keys. Deleting it
-- cascades to its links, which the row trigger above covers.
CREATE FUNCTION bloem_managed_auth_installation_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target integer;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('bloem.managed_identity',0));
 FOR target IN SELECT i.user_id FROM plugin_auth_identities i WHERE i.plugin_installation_id=NEW.id
  UNION SELECT s.user_id FROM plugin_auth_identities i
   JOIN plugin_auth_identities s ON s.external_subject=i.external_subject
   JOIN plugin_installations p ON p.id=s.plugin_installation_id
   WHERE i.plugin_installation_id=NEW.id AND p.plugin_id='silo.auth.oidc' AND p.enabled
  ORDER BY 1
 LOOP
  PERFORM bloem_managed_refresh_identity(target);
 END LOOP;
 RETURN NULL;
END $$;
CREATE TRIGGER bloem_managed_auth_installations AFTER UPDATE OF enabled,plugin_id ON plugin_installations
 FOR EACH ROW WHEN ((OLD.plugin_id='silo.auth.oidc' OR NEW.plugin_id='silo.auth.oidc')
  AND (OLD.enabled IS DISTINCT FROM NEW.enabled OR OLD.plugin_id IS DISTINCT FROM NEW.plugin_id))
 EXECUTE FUNCTION bloem_managed_auth_installation_changed();

-- In-flight snapshots predate the identity fields; the next traversal starts over.
DELETE FROM bloem_managed_snapshots;
-- Only accounts that gain an identity get a new revision.
DO $$ DECLARE target integer; BEGIN
 FOR target IN SELECT DISTINCT account_id FROM bloem_managed_profiles ORDER BY 1 LOOP
  PERFORM bloem_managed_refresh_identity(target);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Republishes every account that had an identity, with it cleared, so the
-- destination stops holding one the host no longer sends.
-- +goose StatementBegin
DO $$ BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('bloem.managed_identity',0));
 PERFORM 1 FROM bloem_managed_clock WHERE singleton FOR UPDATE;
END $$;
CREATE TEMP TABLE bloem_managed_identity_rollback ON COMMIT DROP AS
 SELECT DISTINCT account_id FROM bloem_managed_profiles WHERE identity_subject<>'';
DROP TRIGGER bloem_managed_auth_installations ON plugin_installations;
DROP TRIGGER bloem_managed_auth_identities_update ON plugin_auth_identities;
DROP TRIGGER bloem_managed_auth_identities ON plugin_auth_identities;
DROP FUNCTION bloem_managed_auth_installation_changed();
DROP FUNCTION bloem_managed_auth_identity_changed();
DROP FUNCTION bloem_managed_refresh_identity(integer);
CREATE OR REPLACE FUNCTION bloem_managed_refresh_account(target integer) RETURNS void LANGUAGE plpgsql AS $$
DECLARE rev bigint; tenant uuid; default_id text;
BEGIN
 -- One durable clock lock serializes publication with commits. Snapshots acquire
 -- this fence before copying projection and watermark, never source row locks.
 UPDATE bloem_managed_clock SET revision=revision+1 WHERE singleton RETURNING revision INTO rev;
 FOR tenant IN SELECT organization_id FROM organization_memberships WHERE account_id=target
 UNION SELECT tenant_id FROM bloem_managed_profiles WHERE account_id=target
 LOOP
  SELECT default_profile_id INTO default_id FROM bloem_managed_profiles WHERE tenant_id=tenant AND account_id=target ORDER BY profile_id LIMIT 1;
  IF default_id IS NULL THEN
   SELECT id INTO default_id FROM user_profiles WHERE user_id=target AND organization_id=tenant ORDER BY is_primary DESC,id LIMIT 1;
  END IF;
  UPDATE bloem_managed_profiles SET account_active=false,active=false,account_revision=rev,revision=rev
  WHERE tenant_id=tenant AND account_id=target;
  INSERT INTO bloem_managed_profiles
  SELECT tenant,u.id,p.id,coalesce(default_id,p.id),bloem_managed_display_name(u.username,'Account'),'',bloem_managed_display_name(p.name,'Profile'),
   u.enabled AND m.status='active' AND (o.status='active' OR (o.is_default AND o.status='initializing')),true,rev,rev
  FROM users u JOIN user_profiles p ON p.user_id=u.id
  JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=tenant
  JOIN organizations o ON o.id=tenant
  WHERE u.id=target AND p.organization_id=tenant
  ON CONFLICT(tenant_id,account_id,profile_id) DO UPDATE SET
   default_profile_id=EXCLUDED.default_profile_id,account_name=EXCLUDED.account_name,email=EXCLUDED.email,
   name=EXCLUDED.name,account_active=EXCLUDED.account_active,active=EXCLUDED.active,
   account_revision=rev,revision=rev;
  -- Preserve tombstones but keep account metadata/revision consistent across rows.
  UPDATE bloem_managed_profiles SET default_profile_id=coalesce(default_id,default_profile_id),
   account_name=coalesce((SELECT bloem_managed_display_name(username,'Account') FROM users WHERE id=target),account_name),
   email='',
   account_active=coalesce((SELECT u.enabled AND m.status='active' AND (o.status='active' OR (o.is_default AND o.status='initializing'))
    FROM users u JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=tenant
    JOIN organizations o ON o.id=tenant WHERE u.id=target),false)
  WHERE tenant_id=tenant AND account_id=target;
  INSERT INTO bloem_managed_changes VALUES(rev,tenant,target) ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
DROP FUNCTION bloem_managed_verified_identity(integer);
DELETE FROM bloem_managed_snapshots;
DROP INDEX IF EXISTS bloem_managed_profiles_account;
ALTER TABLE bloem_managed_profiles DROP COLUMN identity_subject, DROP COLUMN identity_issuer;
DO $$ DECLARE target integer; BEGIN
 FOR target IN SELECT account_id FROM bloem_managed_identity_rollback ORDER BY 1 LOOP
  PERFORM bloem_managed_refresh_account(target);
 END LOOP;
END $$;
-- +goose StatementEnd
