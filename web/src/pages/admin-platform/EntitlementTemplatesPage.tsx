import { Archive, ArrowLeft, Copy, History, Plus } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import type { EntitlementTemplate } from "@/api/bloemTypes";
import { useBloemT, type BloemTranslate } from "@/bloem/i18n";
import { useAdminLibraries } from "@/hooks/queries/admin/libraries";
import {
  useArchiveEntitlementTemplate,
  useCloneEntitlementTemplate,
  useCreateEntitlementTemplate,
  useEntitlementTemplateHistory,
  useEntitlementTemplates,
  useRollbackEntitlementTemplate,
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { EntitlementTemplateEditor } from "./EntitlementTemplateEditor";
import { formatDateTime, qualityLabel } from "@/components/admin/policy/policyVocabulary";

function policySummary(bt: BloemTranslate, template: EntitlementTemplate) {
  const policy = template.policy;
  const profiles = bt("bloem.admin.plans.summary.profiles", { count: policy.max_profiles });
  const requests = policy.requests_allowed
    ? bt("bloem.admin.plans.summary.requests")
    : bt("bloem.admin.plans.summary.noRequests");
  if (!policy.playback_allowed) {
    return bt("bloem.admin.plans.summary.browseOnly", { profiles, requests });
  }
  return bt("bloem.admin.plans.summary.withPlayback", {
    screens: bt("bloem.admin.plans.summary.screens", { count: policy.max_streams }),
    quality: qualityLabel(policy.max_playback_quality),
    downloads:
      policy.download_allowed || policy.download_transcode_allowed
        ? bt("bloem.admin.plans.summary.downloads")
        : bt("bloem.admin.plans.summary.noDownloads"),
    profiles,
    requests,
  });
}

function formatTimestamp(bt: BloemTranslate, value?: string) {
  return value ? formatDateTime(value) : bt("bloem.admin.plans.detail.dateNotRecorded");
}

/** A key suggestion for a copy, like "standard-copy". */
function suggestKey(name: string) {
  const slug = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return slug ? `${slug}-copy` : "";
}

export default function EntitlementTemplatesPage() {
  const bt = useBloemT();
  useDocumentTitle(bt("bloem.admin.plans.list.title"));
  const templates = useEntitlementTemplates();
  const libraries = useAdminLibraries();
  const create = useCreateEntitlementTemplate();
  const revise = useReviseEntitlementTemplate();
  const clone = useCloneEntitlementTemplate();
  const archive = useArchiveEntitlementTemplate();
  const rollback = useRollbackEntitlementTemplate();
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [archiveCandidate, setArchiveCandidate] = useState<EntitlementTemplate | null>(null);
  const [cloneCandidate, setCloneCandidate] = useState<EntitlementTemplate | null>(null);
  const [cloneKey, setCloneKey] = useState("");
  const [cloneName, setCloneName] = useState("");
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
          className="-ms-2 w-fit"
          onClick={() => {
            setSelectedKey(null);
            setCreating(false);
            create.reset();
            revise.reset();
          }}
        >
          <ArrowLeft className="size-4" /> {bt("bloem.admin.plans.detail.allPlans")}
        </Button>
        <div className="page-header">
          <div>
            <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">
              {selected?.name ?? bt("bloem.admin.plans.detail.newPlan")}
            </h1>
            <p className="page-subtitle text-sm sm:text-base">
              {selected
                ? bt("bloem.admin.plans.detail.revisionIntro", {
                    revision: selected.revision,
                    nextRevision: selected.revision + 1,
                  })
                : bt("bloem.admin.plans.detail.newPlanIntro")}
            </p>
          </div>
        </div>
        <EntitlementTemplateEditor
          template={selected}
          libraries={(libraries.data ?? []).map(({ id, name }) => ({ id, name }))}
          onSave={save}
          saving={create.isPending || revise.isPending}
          error={selected ? revise.error : create.error}
        />
        {selected ? (
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <History className="size-4" /> {bt("bloem.admin.plans.detail.revisions")}
              </CardTitle>
            </CardHeader>
            <CardContent>
              {history.isLoading ? (
                <Skeleton className="h-8 w-full" />
              ) : (
                <ol className="space-y-3 text-sm">
                  {(history.data ?? []).map((revision) => (
                    <li
                      key={revision.revision}
                      className="border-border flex flex-wrap items-start justify-between gap-3 rounded-lg border p-3"
                    >
                      <div className="space-y-1">
                        <p className="font-medium">
                          {bt(
                            revision.revision === selected.revision
                              ? "bloem.admin.plans.detail.revisionCurrent"
                              : "bloem.admin.plans.detail.revision",
                            { revision: revision.revision },
                          )}
                        </p>
                        <p className="text-muted-foreground">
                          {formatTimestamp(bt, revision.created_at)}
                        </p>
                        <p>{policySummary(bt, revision)}</p>
                      </div>
                      {revision.revision !== selected.revision ? (
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          aria-label={bt("bloem.admin.plans.detail.goBackLabel", {
                            revision: revision.revision,
                          })}
                          disabled={rollback.isPending}
                          onClick={() =>
                            void rollback
                              .mutateAsync({
                                key: selected.key,
                                expected_revision: selected.revision,
                                source_revision: revision.revision,
                                name: selected.name,
                                enabled: selected.enabled,
                              })
                              .catch(() => undefined)
                          }
                        >
                          {bt("bloem.admin.plans.detail.goBack")}
                        </Button>
                      ) : null}
                    </li>
                  ))}
                </ol>
              )}
              {history.isError ? (
                <p className="text-destructive text-sm" role="alert">
                  {bt("bloem.admin.plans.detail.revisionsLoadFailed")}
                </p>
              ) : null}
              {rollback.error ? (
                <p className="text-destructive mt-3 text-sm" role="alert">
                  {rollback.error.message}
                </p>
              ) : null}
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
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">
            {bt("bloem.admin.plans.list.title")}
          </h1>
          <p className="page-subtitle max-w-3xl text-sm sm:text-base">
            {bt("bloem.admin.plans.list.intro")}
          </p>
        </div>
        <Button
          type="button"
          onClick={() => {
            create.reset();
            setCreating(true);
          }}
        >
          <Plus className="size-4" /> {bt("bloem.admin.plans.list.newPlan")}
        </Button>
      </div>
      {templates.isLoading ? (
        <Skeleton className="h-48 w-full" />
      ) : templates.isError ? (
        <p className="text-destructive" role="alert">
          {bt("bloem.admin.plans.list.loadFailed")}
        </p>
      ) : templates.data?.length === 0 ? (
        <p className="text-muted-foreground">{bt("bloem.admin.plans.list.empty")}</p>
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
                    {template.archived
                      ? bt("bloem.admin.plans.list.archived")
                      : template.enabled
                        ? bt("bloem.admin.plans.list.offered")
                        : bt("bloem.admin.plans.list.notOffered")}
                  </Badge>
                </div>
                <p className="text-muted-foreground text-xs">
                  {bt("bloem.admin.plans.list.cardRevision", {
                    revision: template.revision,
                    planKey: template.key,
                    mono: (chunks) => <span className="font-mono">{chunks}</span>,
                  })}
                </p>
              </CardHeader>
              <CardContent className="space-y-4">
                <p className="text-sm">{policySummary(bt, template)}</p>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    aria-label={bt("bloem.admin.plans.list.editLabel", { name: template.name })}
                    onClick={() => {
                      revise.reset();
                      rollback.reset();
                      setSelectedKey(template.key);
                    }}
                  >
                    {bt("bloem.admin.plans.list.edit")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    aria-label={bt("bloem.admin.plans.list.copyLabel", { name: template.name })}
                    onClick={() => {
                      clone.reset();
                      setCloneCandidate(template);
                      setCloneKey(suggestKey(template.name));
                      setCloneName(template.name);
                    }}
                    disabled={clone.isPending}
                  >
                    <Copy className="size-3.5" /> {bt("bloem.admin.plans.list.copy")}
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    aria-label={bt("bloem.admin.plans.list.archiveLabel", { name: template.name })}
                    onClick={() => {
                      archive.reset();
                      setArchiveCandidate(template);
                    }}
                    disabled={template.archived}
                  >
                    <Archive className="size-3.5" /> {bt("bloem.admin.plans.list.archive")}
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.plans.list.offeredOnly", {
          link: (chunks) => (
            <Link className="underline" to="/admin/platform/organizations">
              {chunks}
            </Link>
          ),
        })}
      </p>
      <Dialog
        open={Boolean(cloneCandidate)}
        onOpenChange={(open) => {
          if (!open) {
            setCloneCandidate(null);
            clone.reset();
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {bt("bloem.admin.plans.copyDialog.title", { name: cloneCandidate?.name ?? "" })}
            </DialogTitle>
            <DialogDescription>
              {bt("bloem.admin.plans.copyDialog.description", {
                revision: cloneCandidate?.revision ?? "",
              })}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="space-y-1.5">
              <Label htmlFor="clone-key">{bt("bloem.admin.plans.copyDialog.key")}</Label>
              <Input
                id="clone-key"
                className="font-mono"
                value={cloneKey}
                onChange={(event) => setCloneKey(event.target.value)}
              />
              <p className="text-muted-foreground text-xs">
                {bt("bloem.admin.plans.copyDialog.keyHelp")}
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="clone-name">{bt("bloem.admin.plans.copyDialog.name")}</Label>
              <Input
                id="clone-name"
                value={cloneName}
                onChange={(event) => setCloneName(event.target.value)}
              />
            </div>
            {clone.error ? (
              <p className="text-destructive text-sm" role="alert">
                {clone.error.message || bt("bloem.admin.plans.copyDialog.failed")}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setCloneCandidate(null)}>
              {bt("bloem.admin.plans.copyDialog.cancel")}
            </Button>
            <Button
              type="button"
              disabled={!cloneCandidate || !cloneKey.trim() || !cloneName.trim() || clone.isPending}
              onClick={() => {
                if (!cloneCandidate) return;
                void clone
                  .mutateAsync({
                    key: cloneCandidate.key,
                    source_revision: cloneCandidate.revision,
                    newKey: cloneKey.trim(),
                    name: cloneName.trim(),
                  })
                  .then((result) => {
                    setCloneCandidate(null);
                    setSelectedKey(result.template.key);
                  })
                  .catch(() => undefined);
              }}
            >
              {bt("bloem.admin.plans.copyDialog.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AlertDialog
        open={Boolean(archiveCandidate)}
        onOpenChange={(open) => !open && setArchiveCandidate(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {bt("bloem.admin.plans.archiveDialog.title", { name: archiveCandidate?.name ?? "" })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {bt("bloem.admin.plans.archiveDialog.description")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{bt("bloem.admin.plans.archiveDialog.cancel")}</AlertDialogCancel>
            {archive.error ? (
              <p className="text-destructive text-sm" role="alert">
                {archive.error.message}
              </p>
            ) : null}
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault();
                if (!archiveCandidate) return;
                void archive
                  .mutateAsync({
                    key: archiveCandidate.key,
                    expected_revision: archiveCandidate.revision,
                  })
                  .then(() => setArchiveCandidate(null))
                  .catch(() => undefined);
              }}
              disabled={archive.isPending}
            >
              {bt("bloem.admin.plans.archiveDialog.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
