-- +goose Up
-- +goose StatementBegin
-- Durable projection/outbox; no new required fields on upstream-owned tables.
CREATE TABLE bloem_managed_clock(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0));
INSERT INTO bloem_managed_clock VALUES(true,0);
CREATE TABLE bloem_managed_profiles(
 tenant_id uuid NOT NULL,account_id integer NOT NULL,profile_id text NOT NULL,
 default_profile_id text NOT NULL,account_name text NOT NULL,email text NOT NULL,
 name text NOT NULL,account_active boolean NOT NULL,active boolean NOT NULL,
 account_revision bigint NOT NULL CHECK(account_revision>0),revision bigint NOT NULL CHECK(revision>0),
 PRIMARY KEY(tenant_id,account_id,profile_id));
CREATE TABLE bloem_managed_changes(revision bigint NOT NULL,tenant_id uuid NOT NULL,account_id integer NOT NULL,PRIMARY KEY(revision,tenant_id,account_id));
CREATE INDEX bloem_managed_changes_tenant ON bloem_managed_changes(tenant_id,revision);
CREATE TABLE bloem_managed_grants(
 installation_id bigint PRIMARY KEY REFERENCES plugin_installations(id) ON DELETE CASCADE,
 tenant_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 instance_id uuid NOT NULL DEFAULT gen_random_uuid(),generation bigint NOT NULL DEFAULT 1 CHECK(generation>0),
 active boolean NOT NULL DEFAULT true,acked_revision bigint NOT NULL DEFAULT 0 CHECK(acked_revision>=0));
CREATE TABLE bloem_managed_snapshots(
 id uuid PRIMARY KEY,installation_id bigint NOT NULL REFERENCES bloem_managed_grants(installation_id) ON DELETE CASCADE,
 generation bigint NOT NULL,watermark bigint NOT NULL,ack_token uuid NOT NULL DEFAULT gen_random_uuid(),
 expires_at timestamptz NOT NULL DEFAULT(clock_timestamp()+interval '10 minutes'),delivered boolean NOT NULL DEFAULT false);
CREATE TABLE bloem_managed_snapshot_rows(
 snapshot_id uuid NOT NULL REFERENCES bloem_managed_snapshots(id) ON DELETE CASCADE,
 ordinal integer NOT NULL,profile jsonb NOT NULL,PRIMARY KEY(snapshot_id,ordinal));

CREATE TABLE bloem_managed_page_cursors(snapshot_id uuid NOT NULL REFERENCES bloem_managed_snapshots(id) ON DELETE CASCADE,token uuid NOT NULL,ordinal integer NOT NULL,PRIMARY KEY(snapshot_id,token));

CREATE FUNCTION bloem_managed_refresh_account(target integer) RETURNS void LANGUAGE plpgsql AS $$
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
  SELECT tenant,u.id,p.id,coalesce(default_id,p.id),u.username,coalesce(u.email,''),p.name,
   u.enabled AND m.status='active' AND o.status='active',true,rev,rev
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
   account_name=coalesce((SELECT username FROM users WHERE id=target),account_name),
   email=coalesce((SELECT email FROM users WHERE id=target),email),
   account_active=coalesce((SELECT u.enabled AND m.status='active' AND o.status='active'
    FROM users u JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=tenant
    JOIN organizations o ON o.id=tenant WHERE u.id=target),false)
  WHERE tenant_id=tenant AND account_id=target;
  INSERT INTO bloem_managed_changes VALUES(rev,tenant,target) ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
CREATE FUNCTION bloem_managed_identity_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target integer;
BEGIN
 IF TG_TABLE_NAME='users' THEN target:=coalesce(NEW.id,OLD.id);
 ELSIF TG_TABLE_NAME='user_profiles' THEN target:=coalesce(NEW.user_id,OLD.user_id);
 ELSE target:=coalesce(NEW.account_id,OLD.account_id); END IF;
 PERFORM bloem_managed_refresh_account(target);
 IF TG_TABLE_NAME='user_profiles' THEN
  IF TG_OP='UPDATE' THEN
   IF OLD.user_id IS DISTINCT FROM NEW.user_id THEN PERFORM bloem_managed_refresh_account(OLD.user_id); END IF;
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;RETURN NEW;
END $$;
CREATE TRIGGER bloem_managed_users AFTER INSERT OR DELETE OR UPDATE OF username,email,enabled ON users FOR EACH ROW EXECUTE FUNCTION bloem_managed_identity_changed();
CREATE TRIGGER bloem_managed_profiles AFTER INSERT OR DELETE OR UPDATE OF name,is_primary,user_id,organization_id ON user_profiles FOR EACH ROW EXECUTE FUNCTION bloem_managed_identity_changed();
CREATE TRIGGER bloem_managed_memberships AFTER INSERT OR DELETE OR UPDATE OF status ON organization_memberships FOR EACH ROW EXECUTE FUNCTION bloem_managed_identity_changed();
CREATE FUNCTION bloem_managed_organization_changed() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target integer;
BEGIN
 FOR target IN SELECT account_id FROM organization_memberships WHERE organization_id=NEW.id LOOP
 PERFORM bloem_managed_refresh_account(target); END LOOP;RETURN NEW;
END $$;
CREATE TRIGGER bloem_managed_organizations AFTER UPDATE OF status ON organizations FOR EACH ROW EXECUTE FUNCTION bloem_managed_organization_changed();
DO $$ DECLARE target integer;BEGIN FOR target IN SELECT id FROM users LOOP PERFORM bloem_managed_refresh_account(target); END LOOP;END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER bloem_managed_organizations ON organizations;
DROP FUNCTION bloem_managed_organization_changed();
DROP TRIGGER bloem_managed_memberships ON organization_memberships;
DROP TRIGGER bloem_managed_profiles ON user_profiles;
DROP TRIGGER bloem_managed_users ON users;
DROP FUNCTION bloem_managed_identity_changed();
DROP FUNCTION bloem_managed_refresh_account(integer);
DROP TABLE bloem_managed_page_cursors,bloem_managed_snapshot_rows,bloem_managed_snapshots,bloem_managed_grants,bloem_managed_changes,bloem_managed_profiles,bloem_managed_clock;
-- +goose StatementEnd
