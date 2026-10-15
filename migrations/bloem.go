package migrations

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
)

// BloemFS adapts upstream data migrations to Bloem's finalized membership
// authority without modifying Silo's migration history.
var BloemFS fs.FS = AdaptBloemFS(FS)

// AdaptBloemFS applies owned schema adapters without rewriting upstream SQL.
// It also wraps filtered migration filesystems used by rollback rehearsals.
func AdaptBloemFS(fsys fs.FS) fs.FS {
	if _, ok := fsys.(bloemMigrationFS); ok {
		return fsys
	}
	return bloemMigrationFS{fsys}
}

type bloemMigrationFS struct{ fs.FS }

func (f bloemMigrationFS) Open(name string) (fs.File, error) {
	if name != "sql/20260926181741_request_group_limits.sql" && name != "sql/20261013231605_access_group_max_profiles.sql" && name != "sql/20261013021242_prune_deleted_library_access_references.sql" && name != "sql/20260821170000_entitlement_templates.sql" && name != "sql/20260829085838_membership_policy_isolation.sql" && name != "sql/20260919095615_bloem_policy_library_ids_bigint.sql" && name != "sql/20261006162336_add_login_session_device.sql" {
		return f.FS.Open(name)
	}
	raw, err := fs.ReadFile(f.FS, name)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(string(raw), "-- bloem-schema-adapter:v1:"+name+"\n") {
		return f.adaptedFile(name, raw)
	}
	if name == "sql/20261006162336_add_login_session_device.sql" {
		data := strings.Replace(string(raw), "ADD COLUMN device_id TEXT", "ADD COLUMN IF NOT EXISTS device_id TEXT", 1)
		data = strings.Replace(data, "DROP COLUMN device_platform,\n    DROP COLUMN device_id;", "DROP COLUMN device_platform;", 1)
		return f.adaptedFile(name, []byte(data))
	}
	if name == "sql/20260821170000_entitlement_templates.sql" {
		data := strings.Replace(string(raw), "ADD COLUMN max_profiles integer NOT NULL DEFAULT 0 CHECK (max_profiles >= 0)", "ADD COLUMN IF NOT EXISTS max_profiles integer NOT NULL DEFAULT 5 CHECK (max_profiles >= 0)", 1)
		data = strings.Replace(data, "CREATE UNIQUE INDEX access_groups_managed_template_revision_per_organization_idx", `ALTER TABLE access_groups DROP CONSTRAINT IF EXISTS access_groups_max_profiles_min_check;
ALTER TABLE access_groups ADD CONSTRAINT access_groups_max_profiles_min_check CHECK(max_profiles>=1 OR (managed_template_key IS NOT NULL AND max_profiles=0));
CREATE UNIQUE INDEX access_groups_managed_template_revision_per_organization_idx`, 1)
		return f.adaptedFile(name, []byte(data))
	}
	if name == "sql/20260829085838_membership_policy_isolation.sql" {
		return f.adaptedFile(name, []byte(strings.ReplaceAll(string(raw), "max_profiles integer NOT NULL", "max_profiles integer")))
	}
	if name == "sql/20260919095615_bloem_policy_library_ids_bigint.sql" {
		return f.adaptedFile(name, []byte("-- +goose Up\n-- Policy arrays follow upstream Silo integer[].\nSELECT 1;\n-- +goose Down\nSELECT 1;\n"))
	}

	if name == "sql/20261013021242_prune_deleted_library_access_references.sql" {
		data, err := adaptBloemLibraryPruning(string(raw))
		if err != nil {
			return nil, err
		}
		return f.adaptedFile(name, []byte(data))
	}
	if name == "sql/20261013231605_access_group_max_profiles.sql" {
		data, err := adaptBloemProfileLimits(string(raw))
		if err != nil {
			return nil, err
		}
		return f.adaptedFile(name, []byte(data))
	}
	const original = `UPDATE users SET requests_allowed = false
WHERE id IN (
    SELECT user_id FROM request_user_limits
    WHERE limit_mode = 'blocked' OR approval_mode = 'blocked'
);`
	if strings.Count(string(raw), original) != 1 {
		return nil, fmt.Errorf("Bloem request-group migration adapter: upstream statement changed")
	}
	const replacement = `-- +goose StatementBegin
DO $$
DECLARE writer text := 'v1';
BEGIN
    IF (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized' THEN
        -- This historical migration predates the mirrored-phase helper.
        -- Use the canonical helper when present, otherwise its original marker.
        IF to_regprocedure('public.bloem_membership_policy_writer_marker()') IS NOT NULL THEN
            writer := public.bloem_membership_policy_writer_marker();
        END IF;
        PERFORM set_config('bloem.membership_policy_writer', writer, true);
        -- Legacy account request limits applied to every organization of that account.
        UPDATE public.organization_memberships
        SET requests_allowed = false, access_policy_revision = access_policy_revision + 1
        WHERE account_id IN (
            SELECT user_id FROM request_user_limits
            WHERE limit_mode = 'blocked' OR approval_mode = 'blocked'
        ) AND requests_allowed IS DISTINCT FROM false;
    ELSE
        UPDATE users SET requests_allowed = false
        WHERE id IN (
            SELECT user_id FROM request_user_limits
            WHERE limit_mode = 'blocked' OR approval_mode = 'blocked'
        );
    END IF;
END;
$$;
-- +goose StatementEnd`
	data := []byte(strings.Replace(string(raw), original, replacement, 1))
	return f.adaptedFile(name, data)
}

func (f bloemMigrationFS) adaptedFile(name string, data []byte) (fs.File, error) {
	marker := []byte("-- bloem-schema-adapter:v1:" + name + "\n")
	if !bytes.HasPrefix(data, marker) {
		data = append(marker, data...)
	}

	info, err := fs.Stat(f.FS, name)
	if err != nil {
		return nil, err
	}
	return &bloemMigrationFile{Reader: bytes.NewReader(data), info: bloemMigrationInfo{info, int64(len(data))}}, nil
}

type bloemMigrationFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *bloemMigrationFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *bloemMigrationFile) Close() error               { return nil }

type bloemMigrationInfo struct {
	fs.FileInfo
	size int64
}

func (i bloemMigrationInfo) Size() int64 { return i.size }
