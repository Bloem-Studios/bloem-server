import { Archive, ArrowLeft, Copy, History, Plus } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router";
import type { EntitlementTemplate } from "@/api/bloemTypes";
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
import {
  formatDateTime,
  limitLabel,
  qualityLabel,
} from "@/components/admin/policy/policyVocabulary";

function policySummary(template: EntitlementTemplate) {
  const policy = template.policy;
  const parts = policy.playback_allowed
    ? [
        policy.max_streams === 0 ? "Unlimited screens" : limitLabel(policy.max_streams, "screen"),
        qualityLabel(policy.max_playback_quality),
        policy.download_allowed || policy.download_transcode_allowed ? "Downloads" : "No downloads",
      ]
    : ["Browse only, no playback"];
  parts.push(
    policy.max_profiles === 0 ? "Unlimited profiles" : limitLabel(policy.max_profiles, "profile"),
  );
  parts.push(policy.requests_allowed ? "Requests" : "No requests");
  return parts.join(" · ");
}

function formatTimestamp(value?: string) {
  return value ? formatDateTime(value) : "Date not recorded";
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
  useDocumentTitle("Plans");
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
          <ArrowLeft className="size-4" /> All plans
        </Button>
        <div className="page-header">
          <div>
            <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">
              {selected?.name ?? "New plan"}
            </h1>
            <p className="page-subtitle text-sm sm:text-base">
              {selected
                ? `Revision ${selected.revision}. Saving creates revision ${selected.revision + 1}; accounts stay on the revision they have until you move them.`
                : "Starts with all libraries, 3 screens, 5 profiles and downloads allowed. Change anything before you save."}
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
                <History className="size-4" /> Revisions
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
                          Revision {revision.revision}
                          {revision.revision === selected.revision ? " (current)" : ""}
                        </p>
                        <p className="text-muted-foreground">
                          {formatTimestamp(revision.created_at)}
                        </p>
                        <p>{policySummary(revision)}</p>
                      </div>
                      {revision.revision !== selected.revision ? (
                        <Button
                          type="button"
                          size="sm"
                          variant="outline"
                          aria-label={`Go back to revision ${revision.revision}`}
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
                          Go back to this
                        </Button>
                      ) : null}
                    </li>
                  ))}
                </ol>
              )}
              {history.isError ? (
                <p className="text-destructive text-sm" role="alert">
                  Revisions couldn’t be loaded.
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
          <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">Plans</h1>
          <p className="page-subtitle max-w-3xl text-sm sm:text-base">
            A plan sets what an account gets: libraries, screens, picture quality, downloads and
            requests. Editing a plan never changes accounts already on it; you move them to the new
            revision when you are ready.
          </p>
        </div>
        <Button
          type="button"
          onClick={() => {
            create.reset();
            setCreating(true);
          }}
        >
          <Plus className="size-4" /> New plan
        </Button>
      </div>
      {templates.isLoading ? (
        <Skeleton className="h-48 w-full" />
      ) : templates.isError ? (
        <p className="text-destructive" role="alert">
          Plans couldn’t be loaded. Reload the page to try again.
        </p>
      ) : templates.data?.length === 0 ? (
        <p className="text-muted-foreground">No plans yet. Create one to get started.</p>
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
                    {template.archived ? "Archived" : template.enabled ? "Offered" : "Not offered"}
                  </Badge>
                </div>
                <p className="text-muted-foreground text-xs">
                  Revision {template.revision} · <span className="font-mono">{template.key}</span>
                </p>
              </CardHeader>
              <CardContent className="space-y-4">
                <p className="text-sm">{policySummary(template)}</p>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    aria-label={`Edit ${template.name}`}
                    onClick={() => {
                      revise.reset();
                      rollback.reset();
                      setSelectedKey(template.key);
                    }}
                  >
                    Edit
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    aria-label={`Copy ${template.name}`}
                    onClick={() => {
                      clone.reset();
                      setCloneCandidate(template);
                      setCloneKey(suggestKey(template.name));
                      setCloneName(template.name);
                    }}
                    disabled={clone.isPending}
                  >
                    <Copy className="size-3.5" /> Copy
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    aria-label={`Archive ${template.name}`}
                    onClick={() => {
                      archive.reset();
                      setArchiveCandidate(template);
                    }}
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
        Only offered plans can be given to organizations, accounts and storefront products.{" "}
        <Link className="underline" to="/admin/platform/organizations">
          Go to organizations
        </Link>
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
            <DialogTitle>Copy {cloneCandidate?.name}</DialogTitle>
            <DialogDescription>
              Makes a new plan with the same settings as revision {cloneCandidate?.revision}.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="space-y-1.5">
              <Label htmlFor="clone-key">Key</Label>
              <Input
                id="clone-key"
                className="font-mono"
                value={cloneKey}
                onChange={(event) => setCloneKey(event.target.value)}
              />
              <p className="text-muted-foreground text-xs">
                A short ID for the storefront and the API, like premium-plus. It can’t be changed
                later.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="clone-name">Name</Label>
              <Input
                id="clone-name"
                value={cloneName}
                onChange={(event) => setCloneName(event.target.value)}
              />
            </div>
            {clone.error ? (
              <p className="text-destructive text-sm" role="alert">
                {clone.error.message || "The copy couldn’t be made. Try a different key."}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setCloneCandidate(null)}>
              Cancel
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
              Make copy
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
            <AlertDialogTitle>Archive {archiveCandidate?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Accounts already on this plan keep their settings, and its history stays readable.
              Nobody new can be put on it.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
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
              Archive plan
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
