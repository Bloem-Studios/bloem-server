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

function formatTimestamp(value: string | null) {
  if (!value) return "Never reconciled";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

export function AccountEntitlementPanel({ userID }: { userID: string }) {
  const templates = useEntitlementTemplates(false);
  const detail = useAccountEntitlement(userID);
  const dryRun = useAccountEntitlementDryRun(userID);
  const apply = useApplyAccountEntitlement(userID);
  const [selectedKey, setSelectedKey] = useState("");
  const [preview, setPreview] = useState<EntitlementDryRun | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const effectiveSelectedKey = selectedKey || detail.data?.template_key || "";
  const selected = templates.data?.find((template) => template.key === effectiveSelectedKey);
  const currentTemplateUnavailable =
    Boolean(detail.data?.template_key) &&
    !templates.data?.some((template) => template.key === detail.data?.template_key);

  async function previewChanges() {
    if (!selected) return;
    setAcknowledged(false);
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
    setAcknowledged(false);
    setConfirming(false);
    dryRun.reset();
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Account entitlement</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-muted-foreground text-sm">
          Changes apply only to the managed account group. Custom profile groups are never modified.
        </p>
        {detail.isLoading ? (
          <p role="status" className="text-sm">
            Loading current entitlement…
          </p>
        ) : detail.isError ? (
          <p role="alert" className="text-destructive text-sm">
            {detail.error.message}
          </p>
        ) : detail.data ? (
          <div className="border-border space-y-2 rounded-lg border p-3 text-sm">
            <p className="font-medium">
              {detail.data.template_key && detail.data.template_revision
                ? `${detail.data.template_key} · revision ${detail.data.template_revision}`
                : "No template assigned"}
            </p>
            {detail.data.managed_default_group ? (
              <div className="space-y-2">
                <p>
                  {detail.data.managed_default_group.name} ·{" "}
                  {detail.data.managed_default_group.policy.max_streams} streams ·{" "}
                  {detail.data.managed_default_group.policy.max_profiles} profiles ·{" "}
                  {detail.data.managed_default_group.policy.max_transcodes} transcodes
                </p>
                <EntitlementPolicyDetails policy={detail.data.managed_default_group.policy} />
              </div>
            ) : (
              <p>No managed account group</p>
            )}
            <p>
              {detail.data.library_ids === null
                ? "All enabled libraries"
                : detail.data.library_ids.length > 0
                  ? `Libraries ${detail.data.library_ids.join(", ")}`
                  : "No libraries"}
            </p>
            <p>Last reconciliation: {formatTimestamp(detail.data.last_reconciled_at)}</p>
          </div>
        ) : null}
        {templates.isError ? (
          <p role="alert" className="text-destructive text-sm">
            {templates.error.message}
          </p>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor={`account-template-${userID}`}>Template</Label>
            <select
              id={`account-template-${userID}`}
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={effectiveSelectedKey}
              onChange={(event) => {
                setSelectedKey(event.target.value);
                setPreview(null);
                setAcknowledged(false);
                dryRun.reset();
                apply.reset();
              }}
            >
              <option value="">Select a template</option>
              {currentTemplateUnavailable ? (
                <option value={detail.data?.template_key ?? ""} disabled>
                  Current template unavailable
                </option>
              ) : null}
              {(templates.data ?? []).map((template) => (
                <option key={template.key} value={template.key}>
                  {template.name} · rev {template.revision}
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
          Preview account changes
        </Button>
        {dryRun.error ? (
          <p role="alert" className="text-destructive text-sm">
            {dryRun.error.message}
          </p>
        ) : null}
        {preview ? (
          <div className="border-border space-y-3 rounded-lg border p-3">
            {preview.changes.map((change) => (
              <p key={change.field} className="text-sm">
                <span className="font-medium">{change.field}</span>: {String(change.before)} →{" "}
                {String(change.after)}
              </p>
            ))}
            {preview.warnings.map((warning) => (
              <p key={warning} className="text-warning text-sm">
                {warning}
              </p>
            ))}
            <label className="flex items-start gap-2 text-sm">
              <input
                type="checkbox"
                checked={acknowledged}
                onChange={(event) => setAcknowledged(event.target.checked)}
              />
              I understand this updates only the managed account group.
            </label>
            <Button
              type="button"
              variant="destructive"
              disabled={!acknowledged || !preview.changed}
              onClick={() => setConfirming(true)}
            >
              Apply to account
            </Button>
          </div>
        ) : null}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Apply this account entitlement?</AlertDialogTitle>
              <AlertDialogDescription>
                This uses the reviewed dry run and preserves every custom profile group.
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
                Confirm account apply
              </AlertDialogAction>
            </AlertDialogFooter>
            {apply.error ? (
              <p role="alert" className="text-destructive text-sm">
                {apply.error.message}
              </p>
            ) : null}
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
