import { useState } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import {
  useAccountEntitlement,
  useAccountEntitlementDryRun,
  useApplyAccountEntitlement,
  useEntitlementTemplates,
  type EntitlementDryRun,
} from "@/hooks/queries/admin/entitlementTemplates";
import { EntitlementPolicyDetails } from "./EntitlementPolicyDetails";
import { PolicyValueChange } from "@/components/admin/policy/PolicyValueChange";
import { formatDateTime, libraryListLabel } from "@/components/admin/policy/policyVocabulary";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";

function formatTimestamp(value: string | null) {
  return value ? formatDateTime(value) : "Not yet";
}

export function AccountEntitlementPanel({ userID }: { userID: string }) {
  const templates = useEntitlementTemplates(false);
  const detail = useAccountEntitlement(userID);
  const dryRun = useAccountEntitlementDryRun(userID);
  const apply = useApplyAccountEntitlement(userID);
  const [selectedKey, setSelectedKey] = useState("");
  const [preview, setPreview] = useState<EntitlementDryRun | null>(null);
  const [confirming, setConfirming] = useState(false);
  const libraries = useAdminLibraries();
  const effectiveSelectedKey = selectedKey || detail.data?.template_key || "";
  const selected = templates.data?.find((template) => template.key === effectiveSelectedKey);
  const currentTemplateUnavailable =
    Boolean(detail.data?.template_key) &&
    !templates.data?.some((template) => template.key === detail.data?.template_key);

  async function previewChanges() {
    if (!selected) return;
    setPreview(
      await dryRun.mutateAsync({
        template_key: selected.key,
        template_revision: selected.revision,
      }),
    );
  }

  async function applyChanges() {
    if (!selected || !preview) return;
    await apply.mutateAsync({
      template_key: selected.key,
      template_revision: selected.revision,
      dry_run_token: preview.dry_run_token,
      idempotency_key: crypto.randomUUID(),
    });
    setPreview(null);
    setConfirming(false);
    dryRun.reset();
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Plan</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-muted-foreground text-sm">
          Choosing a plan changes this account’s settings. Profiles with their own settings stay as
          they are. You see the changes before they apply.
        </p>
        {detail.isLoading ? (
          <p role="status" className="text-sm">
            Loading…
          </p>
        ) : detail.isError ? (
          <p role="alert" className="text-destructive text-sm">
            This account couldn’t be loaded. Check the account number.
          </p>
        ) : detail.data ? (
          <div className="border-border space-y-2 rounded-lg border p-3 text-sm">
            <p className="font-medium">
              {detail.data.template_key && detail.data.template_revision
                ? `Plan ${detail.data.template_key}, revision ${detail.data.template_revision}`
                : "No plan yet"}
            </p>
            {detail.data.managed_default_group ? (
              <div className="space-y-2">
                <EntitlementPolicyDetails policy={detail.data.managed_default_group.policy} />
              </div>
            ) : (
              <p className="text-muted-foreground">No settings yet</p>
            )}
            <p>Libraries: {libraryListLabel(detail.data.library_ids, libraries.data ?? [])}</p>
            <p className="text-muted-foreground">
              Last applied: {formatTimestamp(detail.data.last_reconciled_at)}
            </p>
          </div>
        ) : null}
        {templates.isError ? (
          <p role="alert" className="text-destructive text-sm">
            Plans couldn’t be loaded.
          </p>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor={`account-template-${userID}`}>Plan</Label>
            <select
              id={`account-template-${userID}`}
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={effectiveSelectedKey}
              onChange={(event) => {
                setSelectedKey(event.target.value);
                setPreview(null);
                dryRun.reset();
                apply.reset();
              }}
            >
              <option value="">Choose a plan</option>
              {currentTemplateUnavailable ? (
                <option value={detail.data?.template_key ?? ""} disabled>
                  Current plan is no longer offered
                </option>
              ) : null}
              {(templates.data ?? []).map((template) => (
                <option key={template.key} value={template.key}>
                  {template.name} (revision {template.revision})
                </option>
              ))}
            </select>
          </div>
        )}
        <Button
          type="button"
          disabled={!selected || dryRun.isPending}
          onClick={() => void previewChanges().catch(() => undefined)}
        >
          {dryRun.isPending ? "Checking…" : "Preview changes"}
        </Button>
        {dryRun.error ? (
          <p role="alert" className="text-destructive text-sm">
            The preview couldn’t be made. Try again.
          </p>
        ) : null}
        {preview ? (
          <div className="border-border space-y-3 rounded-lg border p-3">
            <p className="text-sm font-medium">
              {preview.changed
                ? "These settings change"
                : "Nothing changes: it already uses this plan"}
            </p>
            {preview.changes.map((change) => (
              <PolicyValueChange key={change.field} change={change} />
            ))}
            {preview.warnings.map((warning) => (
              <p key={warning} className="text-warning text-sm">
                {warning}
              </p>
            ))}
            {preview.changed ? (
              <Button type="button" onClick={() => setConfirming(true)}>
                Apply
              </Button>
            ) : null}
          </div>
        ) : null}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                Put this account on {selected?.name ?? "this plan"}?
              </AlertDialogTitle>
              <AlertDialogDescription>
                The account gets the changes you reviewed. Profiles with their own settings stay as
                they are.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction
                disabled={apply.isPending}
                onClick={(event) => {
                  event.preventDefault();
                  void applyChanges().catch(() => undefined);
                }}
              >
                Apply
              </AlertDialogAction>
            </AlertDialogFooter>
            {apply.error ? (
              <p role="alert" className="text-destructive text-sm">
                {apply.error.message || "The plan couldn’t be applied. Preview again and retry."}
              </p>
            ) : null}
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
