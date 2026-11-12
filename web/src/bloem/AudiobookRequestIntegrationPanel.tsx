import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { AdminV2ClientError } from "@/api/adminV2Client";
import {
  audiobookAcquisitionsApi,
  type AudiobookConnectionInput,
  type AudiobookIntegration,
} from "@/api/bloemAudiobookAcquisitions";
import type { RequestIntegrationTestResult } from "@/api/bloemBookRequests";
import { useBloemT, type BloemMessageKey } from "@/bloem/i18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAudiobookAdminScope } from "./useAudiobookAdminScope";
import { AudiobookRequestConfigForm } from "./AudiobookRequestConfigForm";
import { audiobookConfigDefaults } from "./audiobookRequestFields";
// Credential keys are sent to the server; their labels are catalog keys.
const credentialFields: [string, BloemMessageKey][] = [
  [
    "qbittorrent_password",
    "bloem.admin.bookRequests.audiobookSetup.credential.qbittorrentPassword",
  ],
  ["nzbget_password", "bloem.admin.bookRequests.audiobookSetup.credential.nzbgetPassword"],
  ["abook_email", "bloem.admin.bookRequests.audiobookSetup.credential.abookEmail"],
  ["abook_password", "bloem.admin.bookRequests.audiobookSetup.credential.abookPassword"],
  ["ai_api_key", "bloem.admin.bookRequests.audiobookSetup.credential.aiApiKey"],
];
interface ConnectionEditor {
  id: string;
  name: string;
  values: AudiobookConnectionInput["plugin_config"];
  credentials: Record<string, string>;
}
function editorFor(integration: AudiobookIntegration): ConnectionEditor {
  return {
    id: integration.id,
    name: integration.name,
    values: { ...audiobookConfigDefaults(), ...integration.plugin_config },
    credentials: {},
  };
}
function enteredCredentials(credentials: Record<string, string>) {
  return Object.fromEntries(Object.entries(credentials).filter(([, value]) => value !== ""));
}
// A divider glyph between facts, the same in every language.
const DIVIDER = " · ";
// Download receipts are tied to the client URL they were handed to.
// The labels are product names, shown as they are in every language.
const downloadClientURLs = [
  ["qbittorrent_url", "qBittorrent"],
  ["nzbget_url", "NZBGet"],
] as const;
function movedDownloadClients(editor: ConnectionEditor, saved: AudiobookIntegration) {
  return downloadClientURLs
    .filter(
      ([key]) =>
        String(editor.values[key] ?? "").trim() !== String(saved.plugin_config?.[key] ?? "").trim(),
    )
    .map(([, label]) => label);
}
export function AudiobookRequestIntegrationPanel() {
  const bt = useBloemT();
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
  const [editor, setEditor] = useState<ConnectionEditor | null>(null);
  const [editMessage, setEditMessage] = useState("");
  const [tested, setTested] = useState<{
    id: string;
    result: RequestIntegrationTestResult;
  } | null>(null);
  const integration =
    list.data?.integrations.find((row) => row.id === selected) ?? list.data?.integrations[0];
  const plugin =
    list.data?.installations.find((row) => `${row.id}:${row.capability_id}` === installation) ??
    list.data?.installations[0];
  // The Bookwarehouse storage installation downloads into Bookwarehouse with
  // its own connection: no settings, credentials or download folders.
  const nativePlugin = plugin?.native === true;
  const nativeIntegration = integration?.native === true;
  const library = libraryDraft ?? String(integration?.library_id || "");
  const roots = rootsDraft ?? integration?.download_roots?.join("\n") ?? "";
  const selectedLibrary = list.data?.libraries.find((row) => String(row.id) === library);
  const onError = (error: Error, create = false) => {
    if (!scope.current()) return;
    setBlocked(true);
    if (create && !(error instanceof AdminV2ClientError)) {
      setUnknownCreate(true);
      setMessage(bt("bloem.admin.bookRequests.audiobookSetup.createUnknown"));
    } else
      setMessage(
        error instanceof AdminV2ClientError
          ? bt("bloem.admin.bookRequests.audiobookSetup.actionRefused", { error: error.message })
          : bt("bloem.admin.bookRequests.audiobookSetup.saveUnknown"),
      );
  };
  const create = useMutation({
    retry: false,
    gcTime: 0,
    mutationFn: () => {
      if (!scope.editing || !scope.current() || !plugin || blocked || unknownCreate)
        throw new Error(bt("bloem.admin.bookRequests.audiobookSetup.reloadBeforeCreate"));
      return audiobookAcquisitionsApi.createIntegration(
        {
          name,
          installation_id: plugin.id,
          capability_id: plugin.capability_id,
          plugin_config: nativePlugin ? {} : values,
          credentials: nativePlugin ? {} : credentials,
        },
        scope.context,
      );
    },
    onSuccess: (created) => {
      if (!scope.current()) return;
      setSelected(created.id);
      setLibraryDraft(null);
      setRootsDraft(null);
      setName("");
      setValues(audiobookConfigDefaults());
      setMessage(bt("bloem.admin.bookRequests.audiobookSetup.created"));
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
        throw new Error(bt("bloem.admin.bookRequests.audiobookSetup.reloadBeforeLibrary"));
      return audiobookAcquisitionsApi.setLibrary(
        integration.id,
        selectedLibrary.id,
        nativeIntegration
          ? []
          : roots
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
      setMessage(bt("bloem.admin.bookRequests.audiobookSetup.librarySaved"));
      void qc.invalidateQueries({ queryKey });
    },
    onError: (error) => onError(error),
  });
  const update = useMutation({
    retry: false,
    gcTime: 0,
    mutationFn: () => {
      if (!scope.editing || !scope.current() || !editor || blocked)
        throw new Error(bt("bloem.admin.bookRequests.audiobookSetup.reloadBeforeSave"));
      return audiobookAcquisitionsApi.updateIntegration(
        editor.id,
        {
          name: editor.name,
          plugin_config: editor.values,
          credentials: enteredCredentials(editor.credentials),
        },
        scope.context,
      );
    },
    onSuccess: () => {
      if (!scope.current()) return;
      setEditor(null);
      setEditMessage("");
      setTested(null);
      setMessage(bt("bloem.admin.bookRequests.audiobookSetup.saved"));
      void qc.invalidateQueries({ queryKey });
    },
    onError: (error) => {
      if (!scope.current()) return;
      // A refused edit changed nothing, so it stays in the editor.
      if (error instanceof AdminV2ClientError && error.status === 400)
        setEditMessage(error.message);
      else onError(error);
    },
    onSettled: () => {
      if (scope.current())
        setEditor((current) => (current ? { ...current, credentials: {} } : current));
    },
  });
  const test = useMutation({
    retry: false,
    mutationFn: (id: string) => {
      if (!scope.editing || !scope.current() || blocked)
        throw new Error(bt("bloem.admin.bookRequests.audiobookSetup.reloadBeforeTest"));
      return audiobookAcquisitionsApi.testIntegration(id, scope.context);
    },
    onMutate: () => setTested(null),
    onSuccess: (result, id) => {
      if (scope.current()) setTested({ id, result });
    },
  });
  const busy =
    create.isPending ||
    binding.isPending ||
    update.isPending ||
    test.isPending ||
    scope.switching ||
    list.isError ||
    blocked;
  const createBusy = busy || unknownCreate;
  const refresh = async () => {
    setCredentials({});
    setEditor(null);
    setEditMessage("");
    setTested(null);
    const result = await list.refetch();
    if (!scope.current() || result.isError) return;
    setBlocked(false);
    setLibraryDraft(null);
    setRootsDraft(null);
    if (!unknownCreate) setMessage("");
  };
  const moved = editor && integration ? movedDownloadClients(editor, integration) : [];
  return (
    <section
      className="space-y-4 rounded-xl border p-4 sm:p-5"
      aria-label={bt("bloem.admin.bookRequests.audiobookSetup.title")}
    >
      <header className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-xl font-semibold">
          {bt("bloem.admin.bookRequests.audiobookSetup.title")}
        </h2>
        <Button
          variant="outline"
          size="sm"
          disabled={list.isFetching || create.isPending || binding.isPending || update.isPending}
          onClick={() => void refresh()}
        >
          {bt("bloem.admin.bookRequests.audiobookSetup.reload")}
        </Button>
      </header>
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.bookRequests.audiobookSetup.intro")}
      </p>
      {list.isPending && (
        <p role="status">{bt("bloem.admin.bookRequests.audiobookSetup.loading")}</p>
      )}
      {list.isError && (
        <p role="alert">{bt("bloem.admin.bookRequests.audiobookSetup.loadFailed")}</p>
      )}
      {message && <p role={blocked ? "alert" : "status"}>{message}</p>}
      {!scope.editing && (
        <p className="text-muted-foreground text-sm">
          {bt("bloem.admin.bookRequests.audiobookSetup.platformAdminOnly")}
        </p>
      )}
      {list.data?.available === false && (
        <p role="status">{bt("bloem.admin.bookRequests.audiobookSetup.unavailable")}</p>
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
              {bt("bloem.admin.bookRequests.audiobookSetup.connection")}
              <select
                className="border-input h-9 min-w-0 rounded-md border bg-transparent px-3"
                value={integration.id}
                disabled={create.isPending || binding.isPending || update.isPending}
                onChange={(event) => {
                  setSelected(event.target.value);
                  setLibraryDraft(null);
                  setRootsDraft(null);
                  setEditor(null);
                  setEditMessage("");
                  test.reset();
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
              {integration.ready
                ? bt("bloem.admin.bookRequests.audiobookSetup.ready")
                : bt("bloem.admin.bookRequests.audiobookSetup.notReady")}
              {DIVIDER +
                (integration.library_name ||
                  bt("bloem.admin.bookRequests.audiobookSetup.libraryBindingRequired"))}
            </p>
            {scope.editing && (
              <div className="flex flex-wrap items-center gap-3 sm:col-span-2">
                <Button
                  type="button"
                  variant="outline"
                  disabled={busy}
                  onClick={() => test.mutate(integration.id)}
                >
                  {test.isPending
                    ? bt("bloem.admin.bookRequests.connectionTest.testing")
                    : bt("bloem.admin.bookRequests.connectionTest.test")}
                </Button>
                {!nativeIntegration && (
                  <Button
                    type="button"
                    variant="outline"
                    disabled={busy || editor?.id === integration.id || !integration.plugin_config}
                    onClick={() => {
                      setEditor(editorFor(integration));
                      setEditMessage("");
                    }}
                  >
                    {bt("bloem.admin.bookRequests.audiobookSetup.edit")}
                  </Button>
                )}
                {!nativeIntegration && !integration.plugin_config && (
                  <p role="status" className="text-muted-foreground text-sm">
                    {bt("bloem.admin.bookRequests.audiobookSetup.settingsUnreadable")}
                  </p>
                )}
                {tested?.id === integration.id && (
                  <p role={tested.result.ok ? "status" : "alert"} className="text-sm">
                    {tested.result.ok
                      ? bt("bloem.admin.bookRequests.connectionTest.works", {
                          message: tested.result.message,
                        })
                      : bt("bloem.admin.bookRequests.connectionTest.failed", {
                          message: tested.result.message,
                        })}
                  </p>
                )}
                {test.isError && <p role="alert">{test.error.message}</p>}
              </div>
            )}
            {scope.editing ? (
              <>
                <label className="grid gap-2 text-sm font-medium">
                  {bt("bloem.admin.bookRequests.audiobookSetup.library")}
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
                    <option value="">
                      {bt("bloem.admin.bookRequests.audiobookSetup.selectLibrary")}
                    </option>
                    {list.data.libraries.map((row) => (
                      <option key={row.id} value={row.id}>
                        {row.name}
                        {row.language ? DIVIDER + row.language : ""}
                      </option>
                    ))}
                  </select>
                </label>
                {!nativeIntegration && (
                  <label className="grid gap-2 text-sm font-medium">
                    {bt("bloem.admin.bookRequests.audiobookSetup.downloadRoots")}
                    <textarea
                      className="border-input focus-visible:ring-ring min-h-20 w-full min-w-0 rounded-md border bg-transparent px-3 py-2 text-sm focus-visible:ring-2 focus-visible:outline-none"
                      value={roots}
                      required
                      disabled={busy}
                      onChange={(event) => setRootsDraft(event.target.value)}
                    />
                    <span className="text-muted-foreground text-xs">
                      {bt("bloem.admin.bookRequests.audiobookSetup.downloadRootsHelp")}
                    </span>
                  </label>
                )}
                {selectedLibrary && !nativeIntegration && (
                  <p className="text-muted-foreground text-sm break-all sm:col-span-2">
                    {bt("bloem.admin.bookRequests.audiobookSetup.libraryPaths", {
                      paths: selectedLibrary.paths.join(DIVIDER),
                    })}
                  </p>
                )}
                <Button
                  type="submit"
                  disabled={busy || !selectedLibrary || (!nativeIntegration && !roots.trim())}
                >
                  {bt("bloem.admin.bookRequests.audiobookSetup.saveLibrary")}
                </Button>
              </>
            ) : nativeIntegration ? null : (
              <p className="text-muted-foreground text-sm break-all sm:col-span-2">
                {bt("bloem.admin.bookRequests.audiobookSetup.downloadRootsSummary", {
                  roots:
                    integration.download_roots?.join(DIVIDER) ||
                    bt("bloem.admin.bookRequests.audiobookSetup.notConfigured"),
                })}
              </p>
            )}
            {list.data.libraries.length === 0 && (
              <p role="status">{bt("bloem.admin.bookRequests.audiobookSetup.noLibraries")}</p>
            )}
          </form>
        ) : (
          <p role="status">{bt("bloem.admin.bookRequests.audiobookSetup.noConnections")}</p>
        ))}
      {scope.editing && list.data?.available && integration && editor?.id === integration.id && (
        <form
          className="space-y-4 border-t pt-4"
          aria-label={bt("bloem.admin.bookRequests.audiobookSetup.editFormLabel")}
          onSubmit={(event) => {
            event.preventDefault();
            if (!busy) update.mutate();
          }}
        >
          <h3 className="font-semibold">
            {bt("bloem.admin.bookRequests.audiobookSetup.editTitle", { name: integration.name })}
          </h3>
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.bookRequests.audiobookSetup.editHelp")}
          </p>
          {moved.length > 0 && (
            <p role="alert" className="text-sm">
              {moved.length === 1
                ? bt("bloem.admin.bookRequests.audiobookSetup.urlChangeWarning", {
                    client: moved[0],
                  })
                : bt("bloem.admin.bookRequests.audiobookSetup.urlChangeWarningBoth", {
                    first: moved[0],
                    second: moved[1],
                  })}
            </p>
          )}
          <label className="grid gap-2 text-sm font-medium sm:max-w-sm">
            {bt("bloem.admin.bookRequests.connectionName")}
            <Input
              value={editor.name}
              required
              maxLength={255}
              disabled={busy}
              onChange={(event) => setEditor({ ...editor, name: event.target.value })}
            />
          </label>
          <AudiobookRequestConfigForm
            values={editor.values}
            setValues={(values) => setEditor({ ...editor, values })}
            disabled={busy}
          />
          <fieldset className="grid gap-4 rounded-lg border p-4 sm:grid-cols-2">
            <legend className="px-2 font-medium">
              {bt("bloem.admin.bookRequests.audiobookSetup.credentials")}
            </legend>
            {credentialFields.map(([key, label]) => (
              <label key={key} className="grid gap-2 text-sm font-medium">
                {bt(label)}
                <Input
                  type="password"
                  autoComplete="off"
                  placeholder={
                    integration.stored_credentials?.includes(key)
                      ? bt("bloem.admin.bookRequests.audiobookSetup.credentialSaved")
                      : bt("bloem.admin.bookRequests.audiobookSetup.credentialNotSet")
                  }
                  value={editor.credentials[key] ?? ""}
                  maxLength={1024}
                  disabled={busy}
                  onChange={(event) =>
                    setEditor({
                      ...editor,
                      credentials: { ...editor.credentials, [key]: event.target.value },
                    })
                  }
                />
              </label>
            ))}
            <p className="text-muted-foreground text-xs sm:col-span-2">
              {bt("bloem.admin.bookRequests.audiobookSetup.savedCredentialsHelp")}
            </p>
          </fieldset>
          {editMessage && <p role="alert">{editMessage}</p>}
          <div className="flex flex-wrap gap-3">
            <Button type="submit" disabled={busy || !editor.name.trim()}>
              {update.isPending
                ? bt("bloem.admin.bookRequests.audiobookSetup.saving")
                : bt("bloem.admin.bookRequests.audiobookSetup.save")}
            </Button>
            <Button
              type="button"
              variant="outline"
              disabled={update.isPending}
              onClick={() => {
                setEditor(null);
                setEditMessage("");
              }}
            >
              {bt("bloem.admin.bookRequests.audiobookSetup.cancel")}
            </Button>
          </div>
        </form>
      )}
      {scope.editing &&
        list.data?.available &&
        !editor &&
        (plugin ? (
          <form
            className="space-y-4 border-t pt-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (!createBusy) create.mutate();
            }}
          >
            <h3 className="font-semibold">{bt("bloem.admin.bookRequests.audiobookSetup.add")}</h3>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="grid gap-2 text-sm font-medium">
                {bt("bloem.admin.bookRequests.audiobookSetup.plugin")}
                <select
                  className="border-input h-9 min-w-0 rounded-md border bg-transparent px-3"
                  value={`${plugin.id}:${plugin.capability_id}`}
                  disabled={createBusy}
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
                {bt("bloem.admin.bookRequests.connectionName")}
                <Input
                  value={name}
                  required
                  maxLength={255}
                  disabled={createBusy}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
            </div>
            {!nativePlugin && (
              <>
                <AudiobookRequestConfigForm
                  values={values}
                  setValues={setValues}
                  disabled={createBusy}
                />
                <fieldset className="grid gap-4 rounded-lg border p-4 sm:grid-cols-2">
                  <legend className="px-2 font-medium">
                    {bt("bloem.admin.bookRequests.audiobookSetup.credentials")}
                  </legend>
                  {credentialFields.map(([key, label]) => (
                    <label key={key} className="grid gap-2 text-sm font-medium">
                      {bt(label)}
                      <Input
                        type="password"
                        autoComplete="off"
                        value={credentials[key] ?? ""}
                        maxLength={1024}
                        disabled={createBusy}
                        onChange={(event) =>
                          setCredentials({ ...credentials, [key]: event.target.value })
                        }
                      />
                    </label>
                  ))}
                  <p className="text-muted-foreground text-xs sm:col-span-2">
                    {bt("bloem.admin.bookRequests.audiobookSetup.newCredentialsHelp")}
                  </p>
                </fieldset>
              </>
            )}
            <Button type="submit" disabled={createBusy}>
              {create.isPending
                ? bt("bloem.admin.bookRequests.audiobookSetup.adding")
                : bt("bloem.admin.bookRequests.audiobookSetup.add")}
            </Button>
          </form>
        ) : (
          <Button asChild variant="outline">
            <Link to="/admin/plugins">
              {bt("bloem.admin.bookRequests.audiobookSetup.installPlugin")}
            </Link>
          </Button>
        ))}
    </section>
  );
}
