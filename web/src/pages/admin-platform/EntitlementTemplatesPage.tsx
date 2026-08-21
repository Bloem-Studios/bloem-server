import { Archive, ArrowLeft, Copy, History, Plus } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import type { EntitlementTemplate } from "@/api/types";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";
import {
  useArchiveEntitlementTemplate,
  useCloneEntitlementTemplate,
  useCreateEntitlementTemplate,
  useEntitlementTemplateHistory,
  useEntitlementTemplates,
  useReviseEntitlementTemplate,
} from "@/hooks/queries/admin/entitlementTemplates";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { EntitlementTemplateEditor } from "./EntitlementTemplateEditor";

function policySummary(template: EntitlementTemplate) {
  const policy = template.policy;
  if (!policy.playback_allowed) return "Browse only";
  return `${policy.max_streams} streams · ${policy.max_profiles} profiles · ${policy.download_allowed ? "downloads" : "no downloads"}`;
}

export default function EntitlementTemplatesPage() {
  useDocumentTitle("Entitlement Templates");
  const templates = useEntitlementTemplates();
  const libraries = useAdminLibraries();
  const create = useCreateEntitlementTemplate();
  const revise = useReviseEntitlementTemplate();
  const clone = useCloneEntitlementTemplate();
  const archive = useArchiveEntitlementTemplate();
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [archiveCandidate, setArchiveCandidate] = useState<EntitlementTemplate | null>(null);
  const selected = templates.data?.find((template) => template.key === selectedKey);
  const history = useEntitlementTemplateHistory(selected?.key);

  async function save(input: Parameters<typeof create.mutateAsync>[0]) {
    if (selected) {
      const saved = await revise.mutateAsync({
        key: selected.key,
        expected_revision: selected.revision,
        input,
      });
      setSelectedKey(saved.template.key);
      return;
    }
    const saved = await create.mutateAsync(input);
    setCreating(false);
    setSelectedKey(saved.template.key);
  }

  if (selected || creating) {
    return (
      <section className="page-shell space-y-6 py-4 sm:py-6">
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="-ml-2 w-fit"
          onClick={() => {
            setSelectedKey(null);
            setCreating(false);
          }}
        >
          <ArrowLeft className="size-4" /> All templates
        </Button>
        <div className="page-header">
          <div>
            <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">
              {selected?.name ?? "New entitlement template"}
            </h1>
            <p className="page-subtitle text-sm sm:text-base">
              {selected
                ? `Revision ${selected.revision} · edits create an immutable new revision.`
                : "Starts with all libraries, 3 streams, 5 profiles, and both download modes enabled."}
            </p>
          </div>
        </div>
        <EntitlementTemplateEditor
          template={selected}
          libraries={(libraries.data ?? []).map(({ id, name }) => ({ id, name }))}
          onSave={save}
          saving={create.isPending || revise.isPending}
        />
        {selected ? (
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <History className="size-4" /> Revision history
              </CardTitle>
            </CardHeader>
            <CardContent>
              {history.isLoading ? (
                <Skeleton className="h-8 w-full" />
              ) : (
                <ol className="text-muted-foreground space-y-1 text-sm">
                  {(history.data ?? []).map((revision) => (
                    <li key={revision.revision}>Revision {revision.revision}</li>
                  ))}
                </ol>
              )}
            </CardContent>
          </Card>
        ) : null}
      </section>
    );
  }

  return (
    <section className="page-shell space-y-6 py-4 sm:py-6">
      <div className="page-header gap-5">
        <div>
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">Entitlement Templates</h1>
          <p className="page-subtitle text-sm sm:text-base">
            Reusable per-member policies for Vondel tenants. Existing tenants change only after a
            dry run and explicit apply.
          </p>
        </div>
        <Button type="button" onClick={() => setCreating(true)}>
          <Plus className="size-4" /> New template
        </Button>
      </div>
      {templates.isLoading ? (
        <Skeleton className="h-48 w-full" />
      ) : templates.isError ? (
        <p className="text-destructive" role="alert">
          {templates.error.message}
        </p>
      ) : templates.data?.length === 0 ? (
        <p className="text-muted-foreground">No entitlement templates yet.</p>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {templates.data?.map((template) => (
            <Card key={template.key} className={template.archived ? "opacity-70" : undefined}>
              <CardHeader className="gap-2">
                <div className="flex items-start justify-between gap-3">
                  <CardTitle>{template.name}</CardTitle>
                  <Badge
                    variant={
                      template.archived ? "outline" : template.enabled ? "secondary" : "outline"
                    }
                  >
                    {template.archived ? "Archived" : template.enabled ? "Enabled" : "Disabled"}
                  </Badge>
                </div>
                <p className="text-muted-foreground text-xs">
                  {template.key} · revision {template.revision}
                </p>
              </CardHeader>
              <CardContent className="space-y-4">
                <p className="text-sm">{policySummary(template)}</p>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => setSelectedKey(template.key)}
                  >
                    Edit
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      void clone
                        .mutateAsync({ key: template.key })
                        .then((result) => setSelectedKey(result.template.key))
                    }
                    disabled={clone.isPending}
                  >
                    <Copy className="size-3.5" /> Clone
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    aria-label={`Archive ${template.name}`}
                    onClick={() => setArchiveCandidate(template)}
                    disabled={template.archived}
                  >
                    <Archive className="size-3.5" /> Archive
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      <p className="text-muted-foreground text-sm">
        Product mappings can only select enabled, non-archived templates.{" "}
        <Link className="underline" to="/admin/platform/organizations">
          Manage tenant applications
        </Link>
        .
      </p>
      <AlertDialog
        open={Boolean(archiveCandidate)}
        onOpenChange={(open) => !open && setArchiveCandidate(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Archive {archiveCandidate?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Archived templates remain available for audit history but cannot be selected for new
              mappings.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                archiveCandidate &&
                void archive
                  .mutateAsync({
                    key: archiveCandidate.key,
                    expected_revision: archiveCandidate.revision,
                  })
                  .then(() => setArchiveCandidate(null))
              }
              disabled={archive.isPending}
            >
              Archive template
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
