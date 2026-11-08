import { useState } from "react";
import { AdminV2ClientError } from "@/api/adminV2Client";
import type { AdminContextKey } from "@/api/bloemTypes";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Button } from "@/components/ui/button";
import type { OrganizationLibraryProjection } from "@/hooks/queries/admin/organizationLibraries";
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
    active: "Restore",
    suspended: "Pause",
    withdraw: "Give back",
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
          ? "Someone else changed this library just now. Check its status and try again."
          : cause instanceof Error
            ? cause.message
            : "The change couldn’t be confirmed. Reload the page and try again.",
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
        title={`${action === "withdraw" ? "Give back" : labels[action]} ${library.name}?`}
        description={
          action === "withdraw"
            ? "Members lose this library for good. Only a platform administrator can share it again. No files are deleted."
            : action === "suspended"
              ? "Members can’t use this library until you restore it. No files are deleted."
              : "Members can use this library again, within their own plan and profile limits."
        }
        confirmLabel={labels[action]}
        variant={action === "active" ? "default" : "destructive"}
        isPending={busy}
        onConfirm={() => void confirm()}
      />
    </div>
  );
}
