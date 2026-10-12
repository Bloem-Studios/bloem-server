-- +goose Up
-- The default organization stays 'initializing' until it has exactly one
-- owner, and Bloem treats it as usable in that state (see bookrequests). The
-- managed-tracking projection only counted 'active' organizations, so a default
-- organization's accounts were projected inactive and could not be enrolled.
-- +goose StatementBegin
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
DO $$ DECLARE target integer;BEGIN
 FOR target IN SELECT DISTINCT m.account_id FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id
  WHERE o.is_default AND o.status='initializing' ORDER BY 1 LOOP PERFORM bloem_managed_refresh_account(target); END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
   account_name=coalesce((SELECT bloem_managed_display_name(username,'Account') FROM users WHERE id=target),account_name),
   email='',
   account_active=coalesce((SELECT u.enabled AND m.status='active' AND o.status='active'
    FROM users u JOIN organization_memberships m ON m.account_id=u.id AND m.organization_id=tenant
    JOIN organizations o ON o.id=tenant WHERE u.id=target),false)
  WHERE tenant_id=tenant AND account_id=target;
  INSERT INTO bloem_managed_changes VALUES(rev,tenant,target) ON CONFLICT DO NOTHING;
 END LOOP;
END $$;
DO $$ DECLARE target integer;BEGIN
 FOR target IN SELECT DISTINCT m.account_id FROM organization_memberships m JOIN organizations o ON o.id=m.organization_id
  WHERE o.is_default AND o.status='initializing' ORDER BY 1 LOOP PERFORM bloem_managed_refresh_account(target); END LOOP;
END $$;
-- +goose StatementEnd
