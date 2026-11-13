-- +goose Up
-- A reset link is issued under the authority the issuer had over a member. A
-- promotion that commits after the link was minted must not leave a link
-- behind that a lower role could have issued, so promoting an account to
-- organization administrator, or to organization owner, retires its link in
-- the same transaction. This mirrors Silo's rule for the platform admin role.
-- +goose StatementBegin
CREATE FUNCTION public.bloem_retire_password_reset_on_org_promotion() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
BEGIN
    IF TG_TABLE_NAME = 'organization_memberships' THEN
        IF NEW.legacy_role = 'admin' THEN
            IF TG_OP = 'INSERT' THEN
                DELETE FROM public.password_reset_tokens WHERE user_id = NEW.account_id;
            ELSIF OLD.legacy_role IS DISTINCT FROM NEW.legacy_role THEN
                DELETE FROM public.password_reset_tokens WHERE user_id = NEW.account_id;
            END IF;
        END IF;
    ELSIF NEW.owner_account_id IS NOT NULL THEN
        IF TG_OP = 'INSERT' THEN
            DELETE FROM public.password_reset_tokens WHERE user_id = NEW.owner_account_id;
        ELSIF OLD.owner_account_id IS DISTINCT FROM NEW.owner_account_id THEN
            DELETE FROM public.password_reset_tokens WHERE user_id = NEW.owner_account_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER bloem_retire_password_reset_on_promotion
AFTER INSERT OR UPDATE OF legacy_role ON public.organization_memberships
FOR EACH ROW EXECUTE FUNCTION public.bloem_retire_password_reset_on_org_promotion();

CREATE TRIGGER bloem_retire_password_reset_on_ownership
AFTER INSERT OR UPDATE OF owner_account_id ON public.organizations
FOR EACH ROW EXECUTE FUNCTION public.bloem_retire_password_reset_on_org_promotion();

-- +goose Down
DROP TRIGGER bloem_retire_password_reset_on_ownership ON public.organizations;
DROP TRIGGER bloem_retire_password_reset_on_promotion ON public.organization_memberships;
DROP FUNCTION public.bloem_retire_password_reset_on_org_promotion();
