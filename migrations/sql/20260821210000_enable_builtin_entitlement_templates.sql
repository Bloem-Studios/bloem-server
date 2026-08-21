-- +goose Up
-- Enable untouched built-in templates for installations that ran the original
-- entitlement migration. Revised, archived, and custom templates retain their
-- operator-selected state.
UPDATE public.entitlement_templates
SET enabled = true,
    updated_at = now()
WHERE key IN ('browse-only', 'viewer', 'standard', 'premium', 'reseller-member')
  AND current_revision = 1
  AND archived = false
  AND enabled = false;

-- +goose Down
UPDATE public.entitlement_templates
SET enabled = false,
    updated_at = now()
WHERE key IN ('browse-only', 'viewer', 'standard', 'premium', 'reseller-member')
  AND current_revision = 1
  AND archived = false
  AND enabled = true;
