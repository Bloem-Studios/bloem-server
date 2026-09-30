package migrations

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
)

// BloemFS adapts upstream data migrations to Bloem's finalized membership
// authority without modifying Silo's migration history.
var BloemFS fs.FS = bloemMigrationFS{FS}

type bloemMigrationFS struct{ fs.FS }

func (f bloemMigrationFS) Open(name string) (fs.File, error) {
	if name != "sql/20260926181741_request_group_limits.sql" {
		return f.FS.Open(name)
	}
	raw, err := fs.ReadFile(f.FS, name)
	if err != nil {
		return nil, err
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
BEGIN
    IF (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'finalized' THEN
        PERFORM set_config('bloem.membership_policy_writer', 'v1', true);
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
