import { useState } from "react";
import { AdminV2ClientError } from "@/api/adminV2Client";
import type { AdminContextKey } from "@/api/bloemTypes";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Button } from "@/components/ui/button";
import type { OrganizationLibraryProjection } from "@/hooks/queries/admin/libraries";
import {
  useChangeOrganizationEntitlement,
  type OrganizationEntitlementAction,
} from "@/hooks/queries/admin/organizationEntitlements";

export function LibraryEntitlementActions({
  library,
  contextKey,
  disabled,
}: {
  library: OrganizationLibraryProjection;
  contextKey: AdminContextKey;
  disabled: boolean;
}) {
  const change = useChangeOrganizationEntitlement(contextKey);
  const [confirmation, setConfirmation] = useState<{
    action: OrganizationEntitlementAction;
    revision: number;
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const grant = library.entitlement;
  if (library.access_kind !== "entitled" || !grant || grant.status === "revoked") return null;

  const labels = {
    active: "Restore access",
    suspended: "Suspend access",
    withdraw: "Withdraw grant",
  };
  const action = confirmation?.action ?? "suspended";
  const busy = disabled || change.isPending;
  const revision = grant.security_revision;

  function ask(nextAction: OrganizationEntitlementAction) {
    setError(null);
    setConfirmation({ action: nextAction, revision });
  }

  async function confirm() {
    if (!confirmation || busy) return;
    try {
      await change.mutateAsync({
        folderId: library.folder_id,
        revision: confirmation.revision,
        action: confirmation.action,
      });
    } catch (cause) {
      setError(
        cause instanceof AdminV2ClientError && cause.status === 409
          ? "This grant changed. Review the refreshed library status before trying again."
          : cause instanceof Error
            ? cause.message
            : "Could not confirm the change. Refresh the library list before trying again.",
      );
    } finally {
      setConfirmation(null);
    }
  }

  return (
    <div className="mt-4 space-y-3" role="group" aria-label={`Manage ${library.name}`}>
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={() => ask(grant.status === "active" ? "suspended" : "active")}
        >
          {grant.status === "active" ? labels.suspended : labels.active}
        </Button>
        <Button size="sm" variant="destructive" disabled={busy} onClick={() => ask("withdraw")}>
          {labels.withdraw}
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      <ConfirmDialog
        open={confirmation !== null}
        onOpenChange={(open) => !open && setConfirmation(null)}
        title={`${labels[action]}: ${library.name}`}
        description={
          action === "withdraw"
            ? "This removes the library from this organization’s media ceiling. You cannot restore a withdrawn grant here; a platform administrator must grant it again. Media files are not deleted."
            : action === "suspended"
              ? "This blocks organization access to this library. You can restore this grant later. Media files are not deleted."
              : "This restores the organization’s grant. Profile and access-group restrictions still apply."
        }
        confirmLabel={labels[action]}
        variant={action === "active" ? "default" : "destructive"}
        isPending={busy}
        onConfirm={() => void confirm()}
      />
    </div>
  );
}
