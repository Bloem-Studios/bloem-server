import { useBloemT } from "@/bloem/i18n";
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
  const bt = useBloemT();
  const change = useChangeOrganizationEntitlement(contextKey);
  const [confirmation, setConfirmation] = useState<{
    action: OrganizationEntitlementAction;
    revision: number;
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const grant = library.entitlement;
  if (library.access_kind !== "entitled" || !grant || grant.status === "revoked") return null;

  const labels = {
    active: bt("bloem.admin.organizations.libraryEntitlements.restore"),
    suspended: bt("bloem.admin.organizations.libraryEntitlements.pause"),
    withdraw: bt("bloem.admin.organizations.libraryEntitlements.giveBack"),
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
          ? bt("bloem.admin.organizations.libraryEntitlements.changedElsewhere")
          : cause instanceof Error
            ? cause.message
            : bt("bloem.admin.organizations.libraryEntitlements.notConfirmed"),
      );
    } finally {
      setConfirmation(null);
    }
  }

  return (
    <div
      className="mt-4 space-y-3"
      role="group"
      aria-label={bt("bloem.admin.organizations.libraryEntitlements.manage", {
        name: library.name,
      })}
    >
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
        title={bt("bloem.admin.organizations.libraryEntitlements.confirmTitle", {
          action: labels[action],
          name: library.name,
        })}
        description={
          action === "withdraw"
            ? bt("bloem.admin.organizations.libraryEntitlements.membersLoseThisLibraryFor")
            : action === "suspended"
              ? bt("bloem.admin.organizations.libraryEntitlements.membersCanTUseThis")
              : bt("bloem.admin.organizations.libraryEntitlements.membersCanUseThisLibrary")
        }
        confirmLabel={labels[action]}
        variant={action === "active" ? "default" : "destructive"}
        isPending={busy}
        onConfirm={() => void confirm()}
      />
    </div>
  );
}
