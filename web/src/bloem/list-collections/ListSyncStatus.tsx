import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  invalidateUserCollectionQueries,
  invalidateAdminCollectionQueries,
} from "@/hooks/queries/collectionSurfaceRefresh";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { listActorKey, listCollectionsApi } from "./api";

export function ListSyncStatus({ id }: { id: string }) {
  const client = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const status = useQuery({
    queryKey: ["bloem-list-binding", ...listActorKey(), id],
    queryFn: () => listCollectionsApi.get(id),
    retry: false,
    refetchInterval: (query) =>
      ["queued", "running"].includes(query.state.data?.status ?? "") ? 2000 : 30000,
  });
  async function act(disconnect: boolean) {
    setBusy(true);
    setError("");
    try {
      if (disconnect) await listCollectionsApi.disconnect(id);
      else await listCollectionsApi.refresh(id);
      await Promise.all([
        client.invalidateQueries({ queryKey: ["bloem-list-binding"] }),
        client.invalidateQueries({ queryKey: ["bloem-linked-collection"] }),
        status.data?.scope === "server"
          ? invalidateAdminCollectionQueries(client)
          : invalidateUserCollectionQueries(client, status.data?.target_id),
      ]);
      setConfirm(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not update this connection");
    } finally {
      setBusy(false);
    }
  }
  const binding = status.data;
  const target = status.data?.target_id;
  const scope = status.data?.scope;
  const success = status.data?.last_success;
  useEffect(() => {
    if (!target || !success) return;
    void (scope === "server"
      ? invalidateAdminCollectionQueries(client)
      : invalidateUserCollectionQueries(client, target));
  }, [client, target, scope, success]);
  return (
    <section
      aria-label="Pastime list synchronization"
      className="surface-panel mb-4 grid gap-3 rounded-xl p-4"
    >
      <div>
        <p className="font-medium">Pastime list · {binding?.status ?? "Checking status…"}</p>
        <p className="text-muted-foreground text-sm">
          Membership follows the source list. Name, artwork and sharing stay in Bloem.
        </p>
      </div>
      {binding ? (
        <>
          <p className="text-sm">
            Last successful refresh:{" "}
            {binding.last_success
              ? new Date(binding.last_success).toLocaleString()
              : "Waiting for first refresh"}
            . {binding.matched} available titles.
          </p>
          {binding.failure ? (
            <p role="alert">
              Refresh failed ({binding.failure}). The last successful collection is retained.
            </p>
          ) : null}
          <p className="text-sm">
            Requests: {binding.requests.delivered} issued, {binding.requests.pending} pending,{" "}
            {binding.requests.blocked} blocked.
          </p>
          {binding.requests.quota > 0 ? (
            <p>Requests waiting for quota: {binding.requests.quota}</p>
          ) : null}
          {binding.unmatched.length > 0 ? (
            <details>
              <summary>{binding.unmatched.length} titles unavailable in your library</summary>
              <ul className="mt-2 space-y-1">
                {binding.unmatched.map((item, index) => (
                  <li key={index}>
                    {item.entry.title} · {item.reason}
                  </li>
                ))}
              </ul>
            </details>
          ) : null}
        </>
      ) : null}
      {binding?.status !== "detached" ? (
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            disabled={busy || !binding || binding.status === "paused"}
            onClick={() => void act(false)}
          >
            Refresh now
          </Button>
          <Button variant="outline" disabled={busy || !binding} onClick={() => setConfirm(true)}>
            Disconnect source
          </Button>
        </div>
      ) : (
        <p>This is now a manual collection.</p>
      )}
      {error || status.error ? (
        <p role="alert" className="text-destructive">
          {error || (status.error instanceof Error ? status.error.message : "Status unavailable")}
        </p>
      ) : null}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title="Disconnect Pastime list?"
        description="Keep the current titles and make this a manual collection. Unsent requests are cancelled. Requests already issued remain unchanged. Unsaved edits in the editor will be discarded."
        confirmLabel="Disconnect"
        onConfirm={() => void act(true)}
      />
    </section>
  );
}
