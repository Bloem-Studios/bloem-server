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
  useApplyTenantEntitlement,
  useEntitlementDryRun,
  useEntitlementTemplates,
  type EntitlementDryRun,
} from "@/hooks/queries/admin/entitlementTemplates";

export function OrganizationEntitlementPanel({ organizationID }: { organizationID: string }) {
  const templates = useEntitlementTemplates(false);
  const dryRun = useEntitlementDryRun(organizationID);
  const apply = useApplyTenantEntitlement(organizationID);
  const [selectedKey, setSelectedKey] = useState("");
  const [preview, setPreview] = useState<EntitlementDryRun | null>(null);
  const [acknowledged, setAcknowledged] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const selected =
    templates.data?.find((template) => template.key === selectedKey) ?? templates.data?.[0];

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
    setConfirming(false);
    setAcknowledged(false);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Tenant entitlement</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-muted-foreground text-sm">
          Preview the managed default-group changes first. Custom groups are never modified.
        </p>
        {templates.isLoading ? (
          <p className="text-sm" role="status">
            Loading templates…
          </p>
        ) : templates.isError ? (
          <p className="text-destructive text-sm" role="alert">
            {templates.error.message}
          </p>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor="tenant-template">Template</Label>
            <select
              id="tenant-template"
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              value={selected?.key ?? ""}
              onChange={(event) => {
                setSelectedKey(event.target.value);
                setPreview(null);
                setAcknowledged(false);
              }}
            >
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
          onClick={() => void previewChanges()}
          disabled={!selected || dryRun.isPending}
        >
          Preview changes
        </Button>
        {preview ? (
          <div className="border-border space-y-3 rounded-lg border p-3">
            <p className="text-sm font-medium">
              {preview.changed ? "Managed default group changes" : "No changes are required"}
            </p>
            {preview.changes.map((change) => (
              <p key={change.field} className="text-muted-foreground text-sm">
                <span className="text-foreground font-medium">{change.field}</span>:{" "}
                {String(change.before)} → {String(change.after)}
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
              />{" "}
              I understand this applies only to the managed default group.
            </label>
            <Button
              type="button"
              variant="destructive"
              disabled={!acknowledged || !preview.changed}
              onClick={() => setConfirming(true)}
            >
              Apply to existing tenant
            </Button>
          </div>
        ) : null}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Apply this entitlement?</AlertDialogTitle>
              <AlertDialogDescription>
                This updates the tenant-managed default group using the reviewed dry run. Custom
                groups stay unchanged.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction disabled={apply.isPending} onClick={() => void applyChanges()}>
                Confirm apply
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
