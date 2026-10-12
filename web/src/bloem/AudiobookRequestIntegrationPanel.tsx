import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { AdminV2ClientError } from "@/api/adminV2Client";
import {
  audiobookAcquisitionsApi,
  type AudiobookConnectionInput,
} from "@/api/bloemAudiobookAcquisitions";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAudiobookAdminScope } from "./useAudiobookAdminScope";
import { AudiobookRequestConfigForm } from "./AudiobookRequestConfigForm";
import { audiobookConfigDefaults } from "./audiobookRequestFields";
const credentialFields = [
  ["qbittorrent_password", "qBittorrent password"],
  ["nzbget_password", "NZBGet password"],
  ["abook_email", "abook.link email"],
  ["abook_password", "abook.link password"],
  ["ai_api_key", "AI API key"],
] as const;
export function AudiobookRequestIntegrationPanel() {
  const scope = useAudiobookAdminScope();
  const qc = useQueryClient();
  const queryKey = ["bloem-audiobook-setup", scope.key];
  const list = useQuery({
    queryKey,
    queryFn: ({ signal }) => audiobookAcquisitionsApi.setup(scope.context, signal),
    enabled: scope.allowed,
    retry: false,
  });
  const [selected, setSelected] = useState("");
  const [libraryDraft, setLibraryDraft] = useState<string | null>(null);
  const [rootsDraft, setRootsDraft] = useState<string | null>(null);
  const [installation, setInstallation] = useState("");
  const [name, setName] = useState("");
  const [values, setValues] =
    useState<AudiobookConnectionInput["plugin_config"]>(audiobookConfigDefaults);
  const [credentials, setCredentials] = useState<Record<string, string>>({});
  const [blocked, setBlocked] = useState(false);
  const [unknownCreate, setUnknownCreate] = useState(false);
  const [message, setMessage] = useState("");
  const integration =
    list.data?.integrations.find((row) => row.id === selected) ?? list.data?.integrations[0];
  const plugin =
    list.data?.installations.find((row) => `${row.id}:${row.capability_id}` === installation) ??
    list.data?.installations[0];
  const library = libraryDraft ?? String(integration?.library_id || "");
  const roots = rootsDraft ?? integration?.download_roots?.join("\n") ?? "";
  const selectedLibrary = list.data?.libraries.find((row) => String(row.id) === library);
  const onError = (error: Error, create = false) => {
    if (!scope.current()) return;
    setBlocked(true);
    if (create && !(error instanceof AdminV2ClientError)) {
      setUnknownCreate(true);
      setMessage(
        "Connection outcome is unknown. Reload connections and inspect them before adding another connection.",
      );
    } else
      setMessage(
        error instanceof AdminV2ClientError
          ? `${error.message} Reload connections before another action.`
          : "The save outcome is unknown. Reload connections before another action.",
      );
  };
  const create = useMutation({
    retry: false,
    gcTime: 0,
    mutationFn: () => {
      if (!scope.editing || !scope.current() || !plugin || blocked)
        throw new Error("Reload this administrative context before creating a connection.");
      return audiobookAcquisitionsApi.createIntegration(
        {
          name,
          installation_id: plugin.id,
          capability_id: plugin.capability_id,
          plugin_config: values,
          credentials,
        },
        scope.context,
      );
    },
    onSuccess: (created) => {
      if (!scope.current()) return;
      setSelected(created.id);
      setName("");
      setValues(audiobookConfigDefaults());
      setMessage("Audiobook connection added. Select its library and explicit download roots.");
      void qc.invalidateQueries({ queryKey });
    },
    onError: (error) => onError(error, true),
    onSettled: () => {
      if (scope.current()) setCredentials({});
    },
  });
  const binding = useMutation({
    retry: false,
    mutationFn: () => {
      if (!scope.editing || !scope.current() || !integration || !selectedLibrary || blocked)
        throw new Error("Reload connections before saving a library.");
      return audiobookAcquisitionsApi.setLibrary(
        integration.id,
        selectedLibrary.id,
        roots
          .split("\n")
          .map((root) => root.trim())
          .filter(Boolean),
        scope.context,
      );
    },
    onSuccess: () => {
      if (!scope.current()) return;
      setLibraryDraft(null);
      setRootsDraft(null);
      setMessage("Audiobook library and download roots saved.");
      void qc.invalidateQueries({ queryKey });
    },
    onError: (error) => onError(error),
  });
  const busy = create.isPending || binding.isPending || scope.switching || list.isError || blocked;
  const refresh = async () => {
    setCredentials({});
    const result = await list.refetch();
    if (!scope.current() || result.isError) return;
    setBlocked(unknownCreate);
    setLibraryDraft(null);
    setRootsDraft(null);
    if (!unknownCreate) setMessage("");
  };
  return (
    <section
      className="space-y-4 rounded-xl border p-4 sm:p-5"
      aria-label="Audiobook acquisition setup"
    >
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-xl font-semibold">Audiobook acquisition setup</h2>
        <Button
          variant="outline"
          size="sm"
          disabled={list.isFetching || create.isPending || binding.isPending}
          onClick={() => void refresh()}
        >
          Reload connections
        </Button>
      </header>
      <p className="text-muted-foreground text-sm">
        Connections deliver approved audiobooks to the linked library. A completed download becomes
        fulfilled only after the host finds it in that library.
      </p>
      {list.isPending && <p role="status">Loading audiobook connections…</p>}
      {list.isError && (
        <p role="alert">Unable to load audiobook connections. Reload to try again.</p>
      )}
      {message && <p role={blocked ? "alert" : "status"}>{message}</p>}
      {!scope.editing && (
        <p className="text-muted-foreground text-sm">
          A platform admin can configure acquisition connections for this organization.
        </p>
      )}
      {list.data?.available === false && (
        <p role="status">Audiobook acquisition setup is unavailable in this organization.</p>
      )}
      {list.data?.available &&
        (integration ? (
          <form
            className="grid gap-4 sm:grid-cols-2"
            onSubmit={(event) => {
              event.preventDefault();
              if (!busy) binding.mutate();
            }}
          >
            <label className="grid gap-2 text-sm font-medium">
              Audiobook connection
              <select
                className="border-input h-9 min-w-0 rounded-md border bg-transparent px-3"
                value={integration.id}
                disabled={create.isPending || binding.isPending}
                onChange={(event) => {
                  setSelected(event.target.value);
                  setLibraryDraft(null);
                  setRootsDraft(null);
                }}
              >
                {list.data.integrations.map((row) => (
                  <option key={row.id} value={row.id}>
                    {row.name}
                  </option>
                ))}
              </select>
            </label>
            <p className="self-end text-sm" role="status">
              {integration.ready ? "Ready" : "Not ready"}
              {integration.library_name
                ? ` · ${integration.library_name}`
                : " · Library binding required"}
            </p>
            {scope.editing ? (
              <>
                <label className="grid gap-2 text-sm font-medium">
                  Audiobook library
                  <select
                    className="border-input h-9 min-w-0 rounded-md border bg-transparent px-3"
                    value={library}
                    required
                    disabled={busy}
                    onChange={(event) => {
                      setLibraryDraft(event.target.value);
                      setRootsDraft("");
                    }}
                  >
                    <option value="">Select an eligible library</option>
                    {list.data.libraries.map((row) => (
                      <option key={row.id} value={row.id}>
                        {row.name}
                        {row.language ? ` · ${row.language}` : ""}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="grid gap-2 text-sm font-medium">
                  Download roots (one per line)
                  <textarea
                    className="border-input focus-visible:ring-ring min-h-20 w-full min-w-0 rounded-md border bg-transparent px-3 py-2 text-sm focus-visible:ring-2 focus-visible:outline-none"
                    value={roots}
                    required
                    disabled={busy}
                    onChange={(event) => setRootsDraft(event.target.value)}
                  />
                  <span className="text-muted-foreground text-xs">
                    Choose explicit absolute roots inside the selected library. The host validates
                    each root.
                  </span>
                </label>
                {selectedLibrary && (
                  <p className="text-muted-foreground text-sm break-all sm:col-span-2">
                    Library paths: {selectedLibrary.paths.join(" · ")}
                  </p>
                )}
                <Button type="submit" disabled={busy || !selectedLibrary || !roots.trim()}>
                  Save audiobook library and roots
                </Button>
              </>
            ) : (
              <p className="text-muted-foreground text-sm break-all sm:col-span-2">
                Download roots: {integration.download_roots?.join(" · ") || "Not configured"}
              </p>
            )}
            {list.data.libraries.length === 0 && (
              <p role="status">No eligible audiobook libraries are available.</p>
            )}
          </form>
        ) : (
          <p role="status">No audiobook connections are configured.</p>
        ))}
      {scope.editing &&
        list.data?.available &&
        (plugin ? (
          <form
            className="space-y-4 border-t pt-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (!busy) create.mutate();
            }}
          >
            <h3 className="font-semibold">Add audiobook connection</h3>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="grid gap-2 text-sm font-medium">
                Installed request plugin
                <select
                  className="border-input h-9 min-w-0 rounded-md border bg-transparent px-3"
                  value={`${plugin.id}:${plugin.capability_id}`}
                  disabled={busy}
                  onChange={(event) => setInstallation(event.target.value)}
                >
                  {list.data.installations.map((row) => (
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
            </div>
            <AudiobookRequestConfigForm values={values} setValues={setValues} disabled={busy} />
            <fieldset className="grid gap-4 rounded-lg border p-4 sm:grid-cols-2">
              <legend className="px-2 font-medium">Credentials</legend>
              {credentialFields.map(([key, label]) => (
                <label key={key} className="grid gap-2 text-sm font-medium">
                  {label}
                  <Input
                    type="password"
                    autoComplete="off"
                    value={credentials[key] ?? ""}
                    maxLength={1024}
                    disabled={busy}
                    onChange={(event) =>
                      setCredentials({ ...credentials, [key]: event.target.value })
                    }
                  />
                </label>
              ))}
              <p className="text-muted-foreground text-xs sm:col-span-2">
                Credentials are write-only and cleared after submission. Leave unused services
                empty.
              </p>
            </fieldset>
            <Button type="submit" disabled={busy}>
              {create.isPending ? "Adding connection…" : "Add audiobook connection"}
            </Button>
          </form>
        ) : (
          <Button asChild variant="outline">
            <Link to="/admin/plugins">Install the audiobook request plugin</Link>
          </Button>
        ))}
    </section>
  );
}
