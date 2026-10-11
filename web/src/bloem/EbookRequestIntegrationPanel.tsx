import { useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Link } from "react-router";
import { ebookRequestsApi, type EbookRequestIntegrations } from "@/api/bloemBookRequests";
import { captureSessionIdentity } from "@/api/client";
import { captureAdminRequestContext } from "@/api/adminV2Client";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";

function allowsBaseURL(value: string, allowHttp: boolean) {
  try {
    const { protocol } = new URL(value);
    return protocol === "https:" || (allowHttp && protocol === "http:");
  } catch {
    return false;
  }
}

export function EbookRequestIntegrationPanel() {
  const { active, switching } = useAdminContext();
  const context = active ? captureAdminRequestContext(active.key) : null;
  const identity = captureSessionIdentity();
  const qc = useQueryClient();
  const scope = [
    "bloem-admin-ebook-request-integrations",
    context?.key,
    context?.generation,
    identity.serverOrigin,
    identity.authContextVersion,
  ] as const;
  const allowed = active?.scope === "organization" && active.authority === "platform_admin";
  const list = useQuery({
    queryKey: scope,
    queryFn: () => ebookRequestsApi.integrations(context),
    enabled: allowed && !switching && !!context,
  });
  const [selected, setSelected] = useState("");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [installation, setInstallation] = useState("");
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [enabledDraft, setEnabledDraft] = useState<boolean | null>(null);
  const enabled = enabledDraft ?? list.data?.enabled ?? false;
  const policyId = useId();
  const [allowHttp, setAllowHttp] = useState(false);
  const httpOptInId = useId();
  const baseUrlAllowed = allowsBaseURL(baseUrl, allowHttp);
  const installations = list.data?.installations ?? [];
  const plugin =
    installations.find((row) => `${row.id}:${row.capability_id}` === installation) ??
    installations[0];
  const integration =
    list.data?.integrations.find((row) => row.id === selected) ?? list.data?.integrations[0];
  const library = integration
    ? (drafts[integration.id] ?? String(integration.library_id ?? ""))
    : "";
  const policy = useMutation({
    mutationFn: () => ebookRequestsApi.setPolicy(enabled, context),
    onSuccess: (saved) => {
      qc.setQueryData<EbookRequestIntegrations>(scope, (current) =>
        current ? { ...current, enabled: saved.enabled } : current,
      );
      setEnabledDraft(null);
      void qc.invalidateQueries({ queryKey: ["bloem-ebook-requests"] });
      toast.success("Ebook request policy saved");
    },
  });
  const save = useMutation({
    mutationFn: ({ id, libraryId }: { id: string; libraryId: number }) =>
      ebookRequestsApi.setLibrary(id, libraryId, context),
    onSuccess: (_, { id }) => {
      setDrafts((current) => {
        const next = { ...current };
        delete next[id];
        return next;
      });
      void qc.invalidateQueries({ queryKey: scope });
      toast.success("Ebook fulfillment library saved");
    },
  });
  const create = useMutation({
    mutationFn: () => {
      if (!plugin) throw new Error("Select an installed Bookwarehouse request plugin.");
      if (!baseUrlAllowed) throw new Error("Use HTTPS or allow HTTP for a trusted local server.");
      return ebookRequestsApi.createIntegration(
        {
          name,
          base_url: baseUrl,
          api_key: apiKey,
          installation_id: plugin.id,
          capability_id: plugin.capability_id,
          allow_http: allowHttp,
        },
        context,
      );
    },
    onSuccess: (created) => {
      setName("");
      setBaseUrl("");
      setApiKey("");
      setAllowHttp(false);
      setSelected(created.id);
      void qc.invalidateQueries({ queryKey: scope });
      toast.success("Ebook fulfillment connection added");
    },
  });
  const busy = save.isPending || create.isPending || policy.isPending || switching;
  if (!allowed) return null;
  return (
    <section className="space-y-4 rounded-xl border p-5" aria-label="Ebook fulfillment setup">
      <h2 className="text-xl font-semibold">Ebook fulfillment</h2>
      <p className="text-muted-foreground text-sm">
        Choose the library where each connection delivers ebooks. A provider reporting success is
        not enough to fulfil a request; the ebook must appear in this library.
      </p>
      {list.isLoading && <p role="status">Loading ebook connections…</p>}
      {list.isError && <p role="alert">{list.error.message}</p>}
      {list.data?.available && (
        <form
          className="space-y-3 border-b pb-4"
          onSubmit={(event) => {
            event.preventDefault();
            if (!busy && enabled !== list.data.enabled) policy.mutate();
          }}
        >
          <label htmlFor={policyId} className="flex items-center gap-2 text-sm font-medium">
            <Checkbox
              id={policyId}
              checked={enabled}
              disabled={busy}
              onCheckedChange={(checked) => {
                setEnabledDraft(checked === true);
                policy.reset();
              }}
            />
            Enable ebook requests
          </label>
          <p className="text-muted-foreground text-sm">
            Bookwarehouse can acquire books for approved requests. Turning this off prevents new
            requests; work already started may continue.
          </p>
          {policy.isError && <p role="alert">{policy.error.message}</p>}
          <Button type="submit" disabled={busy || enabled === list.data.enabled}>
            {policy.isPending ? "Saving policy…" : "Save ebook request policy"}
          </Button>
        </form>
      )}
      {list.data?.available === false ? (
        <p role="status">Ebook fulfillment is available only in the default organization.</p>
      ) : (
        list.data?.available &&
        (integration ? (
          <form
            className="flex flex-wrap items-end gap-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (library && list.data.libraries.some((row) => row.id === Number(library)) && !busy)
                save.mutate({ id: integration.id, libraryId: Number(library) });
            }}
          >
            <label className="grid gap-2 text-sm font-medium">
              Connection
              <select
                className="border-input h-9 rounded-md border bg-transparent px-3"
                value={integration.id}
                disabled={busy}
                onChange={(event) => setSelected(event.target.value)}
              >
                {list.data.integrations.map((row) => (
                  <option key={row.id} value={row.id}>
                    {row.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid gap-2 text-sm font-medium">
              Library
              <select
                className="border-input h-9 rounded-md border bg-transparent px-3"
                value={library}
                required
                disabled={busy}
                onChange={(event) => setDrafts({ ...drafts, [integration.id]: event.target.value })}
              >
                <option value="">Select a library</option>
                {list.data.libraries.map((row) => (
                  <option key={row.id} value={row.id}>
                    {row.name}
                  </option>
                ))}
              </select>
            </label>
            <Button
              type="submit"
              disabled={!library || Number(library) === integration.library_id || busy}
            >
              {save.isPending ? "Saving…" : "Save library"}
            </Button>
            {save.isError && <p role="alert">{save.error.message}</p>}
            {list.data.libraries.length === 0 && (
              <p role="status" className="text-muted-foreground text-sm">
                No eligible ebook libraries are available.
              </p>
            )}
          </form>
        ) : (
          <p role="status" className="text-muted-foreground text-sm">
            No eligible ebook fulfillment connections are configured.
          </p>
        ))
      )}
      {list.data?.available &&
        (plugin ? (
          <form
            className="grid gap-4 border-t pt-4 sm:grid-cols-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (!busy && baseUrlAllowed) create.mutate();
            }}
          >
            <h3 className="font-semibold sm:col-span-2">Add Bookwarehouse connection</h3>
            <p className="text-muted-foreground text-sm sm:col-span-2">
              This connection requests EPUB ebooks. After adding it, choose its destination library
              above.
            </p>
            <label className="grid gap-2 text-sm font-medium">
              Plugin
              <select
                className="border-input h-9 rounded-md border bg-transparent px-3"
                value={`${plugin.id}:${plugin.capability_id}`}
                disabled={busy}
                onChange={(event) => setInstallation(event.target.value)}
              >
                {installations.map((row) => (
                  <option
                    key={`${row.id}:${row.capability_id}`}
                    value={`${row.id}:${row.capability_id}`}
                  >
                    {row.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid gap-2 text-sm font-medium">
              Connection name
              <Input
                value={name}
                required
                maxLength={255}
                disabled={busy}
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            <label className="grid gap-2 text-sm font-medium">
              Base URL
              <Input
                type="url"
                placeholder="https://books.example"
                value={baseUrl}
                required
                maxLength={2048}
                disabled={busy}
                onChange={(event) => setBaseUrl(event.target.value)}
              />
            </label>
            <label className="grid gap-2 text-sm font-medium">
              API key
              <Input
                type="password"
                autoComplete="off"
                value={apiKey}
                required
                maxLength={4096}
                disabled={busy}
                onChange={(event) => setApiKey(event.target.value)}
              />
            </label>
            <label htmlFor={httpOptInId} className="flex items-center gap-2 text-sm sm:col-span-2">
              <Checkbox
                id={httpOptInId}
                checked={allowHttp}
                disabled={busy}
                onCheckedChange={(checked) => setAllowHttp(checked === true)}
              />
              Allow HTTP for a trusted local server
            </label>
            <Button type="submit" disabled={busy || !baseUrlAllowed}>
              {create.isPending ? "Adding…" : "Add connection"}
            </Button>
            {create.isError && <p role="alert">{create.error.message}</p>}
          </form>
        ) : (
          <Button asChild variant="outline">
            <Link to="/admin/plugins">Install the Bookwarehouse request plugin</Link>
          </Button>
        ))}
    </section>
  );
}
