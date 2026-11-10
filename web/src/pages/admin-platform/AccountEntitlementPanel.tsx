import { useState } from "react";
import { useBloemT, type BloemTranslate } from "@/bloem/i18n";
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

function formatTimestamp(bt: BloemTranslate, value: string | null) {
  return value ? formatDateTime(value) : bt("bloem.admin.plans.account.notYet");
}

export function AccountEntitlementPanel({ userID }: { userID: string }) {
  const bt = useBloemT();
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
        <CardTitle>{bt("bloem.admin.plans.account.title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-muted-foreground text-sm">{bt("bloem.admin.plans.account.intro")}</p>
        {detail.isLoading ? (
          <p role="status" className="text-sm">
            {bt("bloem.admin.plans.account.loading")}
          </p>
        ) : detail.isError ? (
          <p role="alert" className="text-destructive text-sm">
            {bt("bloem.admin.plans.account.loadFailed")}
          </p>
        ) : detail.data ? (
          <div className="border-border space-y-2 rounded-lg border p-3 text-sm">
            <p className="font-medium">
              {detail.data.template_key && detail.data.template_revision
                ? bt("bloem.admin.plans.account.current", {
                    plan: detail.data.template_key,
                    revision: detail.data.template_revision,
                  })
                : bt("bloem.admin.plans.account.noPlan")}
            </p>
            {detail.data.managed_default_group ? (
              <div className="space-y-2">
                <EntitlementPolicyDetails policy={detail.data.managed_default_group.policy} />
              </div>
            ) : (
              <p className="text-muted-foreground">{bt("bloem.admin.plans.account.noSettings")}</p>
            )}
            <p>
              {bt("bloem.admin.plans.account.libraries", {
                libraries: libraryListLabel(detail.data.library_ids, libraries.data ?? []),
              })}
            </p>
            <p className="text-muted-foreground">
              {bt("bloem.admin.plans.account.lastApplied", {
                time: formatTimestamp(bt, detail.data.last_reconciled_at),
              })}
            </p>
          </div>
        ) : null}
        {templates.isError ? (
          <p role="alert" className="text-destructive text-sm">
            {bt("bloem.admin.plans.account.plansLoadFailed")}
          </p>
        ) : (
          <div className="space-y-1.5">
            <Label htmlFor={`account-template-${userID}`}>
              {bt("bloem.admin.plans.account.plan")}
            </Label>
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
              <option value="">{bt("bloem.admin.plans.account.choosePlan")}</option>
              {currentTemplateUnavailable ? (
                <option value={detail.data?.template_key ?? ""} disabled>
                  {bt("bloem.admin.plans.account.currentUnavailable")}
                </option>
              ) : null}
              {(templates.data ?? []).map((template) => (
                <option key={template.key} value={template.key}>
                  {bt("bloem.admin.plans.account.option", {
                    name: template.name,
                    revision: template.revision,
                  })}
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
          {dryRun.isPending
            ? bt("bloem.admin.plans.account.checking")
            : bt("bloem.admin.plans.account.preview")}
        </Button>
        {dryRun.error ? (
          <p role="alert" className="text-destructive text-sm">
            {bt("bloem.admin.plans.account.previewFailed")}
          </p>
        ) : null}
        {preview ? (
          <div className="border-border space-y-3 rounded-lg border p-3">
            <p className="text-sm font-medium">
              {preview.changed
                ? bt("bloem.admin.plans.account.changes")
                : bt("bloem.admin.plans.account.noChanges")}
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
                {bt("bloem.admin.plans.account.apply")}
              </Button>
            ) : null}
          </div>
        ) : null}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>
                {selected
                  ? bt("bloem.admin.plans.account.confirmTitle", { name: selected.name })
                  : bt("bloem.admin.plans.account.confirmTitleNoName")}
              </AlertDialogTitle>
              <AlertDialogDescription>
                {bt("bloem.admin.plans.account.confirmDescription")}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{bt("bloem.admin.plans.account.cancel")}</AlertDialogCancel>
              <AlertDialogAction
                disabled={apply.isPending}
                onClick={(event) => {
                  event.preventDefault();
                  void applyChanges().catch(() => undefined);
                }}
              >
                {bt("bloem.admin.plans.account.confirm")}
              </AlertDialogAction>
            </AlertDialogFooter>
            {apply.error ? (
              <p role="alert" className="text-destructive text-sm">
                {apply.error.message || bt("bloem.admin.plans.account.applyFailed")}
              </p>
            ) : null}
          </AlertDialogContent>
        </AlertDialog>
      </CardContent>
    </Card>
  );
}
