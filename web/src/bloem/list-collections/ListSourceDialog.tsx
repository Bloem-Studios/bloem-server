import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useUserLibraries } from "@/hooks/queries/libraries";
import { listCollectionsApi, listActorKey, type ListBinding, type ListCapability } from "./api";

export function ListSourceDialog({
  capability,
  scope = "personal",
  onClose,
  onCreated,
}: {
  capability: ListCapability;
  scope?: "personal" | "server";
  onClose(): void;
  onCreated(binding: ListBinding): void;
}) {
  const [connection, setConnection] = useState(capability.connections[0]?.installation_id ?? 0);
  const [url, setURL] = useState("");
  const [job, setJob] = useState("");
  const [name, setName] = useState("");
  const [shared, setShared] = useState(false);
  const [libraries, setLibraries] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const queryClient = useQueryClient();
  const availableLibraries = useUserLibraries();
  const preview = useQuery({
    queryKey: ["bloem-list-preview", ...listActorKey(), job],
    queryFn: () => listCollectionsApi.getPreview(job),
    enabled: Boolean(job),
    retry: false,
    refetchInterval: (query) =>
      ["pending", "running"].includes(query.state.data?.status ?? "pending") && !query.state.error
        ? 1000
        : false,
  });
  const resolved =
    preview.data?.status === "resolved" && new Date(preview.data.expires_at).getTime() > Date.now();
  async function resolve() {
    setBusy(true);
    setError("");
    try {
      const result = await listCollectionsApi.preview(connection, url);
      setJob(result.job_id);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not preview this list");
    } finally {
      setBusy(false);
    }
  }
  async function create() {
    setBusy(true);
    setError("");
    try {
      const result = await listCollectionsApi.create({
        preview_id: job,
        scope,
        name: name.trim() || preview.data?.name || "Pastime list",
        is_shared: scope === "personal" && shared,
        ...(scope === "server" ? { library_ids: libraries } : {}),
      });
      await queryClient.invalidateQueries();
      onCreated(result);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not link this list");
    } finally {
      setBusy(false);
    }
  }
  const selected = capability.connections.find((entry) => entry.installation_id === connection);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Link a Pastime list</DialogTitle>
          <DialogDescription>
            Titles available to your profile appear in this collection, in the source order. Missing
            titles use your normal request permissions and quota. Refreshes run daily.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <Label htmlFor="pastime-connection">Pastime connection</Label>
          <select
            id="pastime-connection"
            className="bg-background rounded-md border p-2"
            value={connection}
            disabled={busy || Boolean(job)}
            onChange={(event) => setConnection(Number(event.target.value))}
          >
            {capability.connections.map((entry) => (
              <option key={entry.installation_id} value={entry.installation_id}>
                {entry.origin}
              </option>
            ))}
          </select>
          <Label htmlFor="pastime-list-url">List URL</Label>
          <Input
            id="pastime-list-url"
            type="url"
            placeholder={`${selected?.origin ?? ""}/lists/…`}
            value={url}
            disabled={busy || Boolean(job)}
            onChange={(event) => setURL(event.target.value)}
          />
          {!job ? (
            <Button onClick={() => void resolve()} disabled={busy || !url || !connection}>
              {busy ? "Checking…" : "Preview list"}
            </Button>
          ) : null}
          {job && !preview.data && !preview.error ? (
            <p role="status">Checking list access…</p>
          ) : null}
          {preview.data ? (
            <p role="status">
              {resolved
                ? `${preview.data.name} · ${preview.data.count} titles`
                : ["pending", "running"].includes(preview.data.status)
                  ? "Checking list access…"
                  : "Preview unavailable. Check access and try again."}
            </p>
          ) : null}
          {resolved ? (
            <>
              <Label htmlFor="pastime-collection-name">Collection name</Label>
              <Input
                id="pastime-collection-name"
                value={name || preview.data?.name || ""}
                onChange={(event) => setName(event.target.value)}
                maxLength={200}
              />
              {scope === "personal" ? (
                <label className="flex items-start gap-2">
                  <input
                    type="checkbox"
                    checked={shared}
                    onChange={(event) => setShared(event.target.checked)}
                  />
                  Share with other profiles on my account
                </label>
              ) : (
                <fieldset className="grid gap-2">
                  <legend className="mb-2 font-medium">Include titles from these libraries</legend>
                  {availableLibraries.data?.map((library) => (
                    <label key={library.id} className="flex items-center gap-2">
                      <input
                        type="checkbox"
                        checked={libraries.includes(library.id)}
                        onChange={(event) =>
                          setLibraries((current) =>
                            event.target.checked
                              ? [...current, library.id]
                              : current.filter((id) => id !== library.id),
                          )
                        }
                      />
                      {library.name}
                    </label>
                  ))}
                </fieldset>
              )}
              <p className="text-muted-foreground text-sm">
                {scope === "personal"
                  ? shared
                    ? "Shared personal collection. Requests are attributed to your profile."
                    : "Personal collection, visible only to your profile."
                  : "Server collection. Requests are attributed to your profile."}
              </p>
            </>
          ) : null}
          {error || preview.error ? (
            <p role="alert" className="text-destructive">
              {error ||
                (preview.error instanceof Error ? preview.error.message : "Preview unavailable")}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          {job ? (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => {
                setJob("");
                setError("");
              }}
            >
              Change source
            </Button>
          ) : null}
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          {resolved ? (
            <Button
              disabled={
                busy ||
                (scope === "server" && (!selected?.can_create_server || libraries.length === 0))
              }
              onClick={() => void create()}
            >
              {busy ? "Linking…" : "Link collection"}
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
