import { useId, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Link } from "react-router";
import {
  ebookRequestsApi,
  type EbookRequestIntegrations,
  type RequestIntegrationTestResult,
} from "@/api/bloemBookRequests";
import { captureSessionIdentity } from "@/api/client";
import { captureAdminRequestContext } from "@/api/adminV2Client";
import { useBloemT } from "@/bloem/i18n";
import { useAdminContext } from "@/contexts/AdminContextProvider";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";

// An example address, the same in every language.
const EXAMPLE_BASE_URL = "https://books.example";

function allowsBaseURL(value: string, allowHttp: boolean) {
  try {
    const { protocol } = new URL(value);
    return protocol === "https:" || (allowHttp && protocol === "http:");
  } catch {
    return false;
  }
}

export function EbookRequestIntegrationPanel() {
  const bt = useBloemT();
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
  const allowed = active?.authority === "platform_admin";
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
  const installations = list.data?.installations ?? [];
  const plugin =
    installations.find((row) => `${row.id}:${row.capability_id}` === installation) ??
    installations[0];
  // The Bookwarehouse storage installation uses its own connection.
  const native = plugin?.native === true;
  const baseUrlAllowed = native || allowsBaseURL(baseUrl, allowHttp);
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
      toast.success(bt("bloem.admin.bookRequests.ebookSetup.policySaved"));
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
      toast.success(bt("bloem.admin.bookRequests.ebookSetup.librarySaved"));
    },
  });
  const create = useMutation({
    mutationFn: () => {
      if (!plugin) throw new Error(bt("bloem.admin.bookRequests.ebookSetup.selectPlugin"));
      if (!baseUrlAllowed) throw new Error(bt("bloem.admin.bookRequests.ebookSetup.httpsRequired"));
      return ebookRequestsApi.createIntegration(
        native
          ? {
              name,
              base_url: "",
              api_key: "",
              installation_id: plugin.id,
              capability_id: plugin.capability_id,
            }
          : {
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
      toast.success(bt("bloem.admin.bookRequests.ebookSetup.created"));
    },
  });
  const [tested, setTested] = useState<{
    id: string;
    result: RequestIntegrationTestResult;
  } | null>(null);
  const test = useMutation({
    mutationFn: (id: string) => ebookRequestsApi.testIntegration(id, context),
    onMutate: () => setTested(null),
    onSuccess: (result, id) => setTested({ id, result }),
  });
  const busy =
    save.isPending || create.isPending || policy.isPending || test.isPending || switching;
  if (!allowed) return null;
  return (
    <section
      className="space-y-4 rounded-xl border p-5"
      aria-label={bt("bloem.admin.bookRequests.ebookSetup.sectionLabel")}
    >
      <h2 className="text-xl font-semibold">{bt("bloem.admin.bookRequests.ebookSetup.title")}</h2>
      <p className="text-muted-foreground text-sm">
        {bt("bloem.admin.bookRequests.ebookSetup.intro")}
      </p>
      {list.isLoading && <p role="status">{bt("bloem.admin.bookRequests.ebookSetup.loading")}</p>}
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
            {bt("bloem.admin.bookRequests.ebookSetup.enable")}
          </label>
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.bookRequests.ebookSetup.enableHelp")}
          </p>
          {policy.isError && <p role="alert">{policy.error.message}</p>}
          <Button type="submit" disabled={busy || enabled === list.data.enabled}>
            {policy.isPending
              ? bt("bloem.admin.bookRequests.ebookSetup.savingPolicy")
              : bt("bloem.admin.bookRequests.ebookSetup.savePolicy")}
          </Button>
        </form>
      )}
      {list.data?.available === false ? (
        <p role="status">{bt("bloem.admin.bookRequests.ebookSetup.defaultOrganizationOnly")}</p>
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
              {bt("bloem.admin.bookRequests.ebookSetup.connection")}
              <select
                className="border-input h-9 rounded-md border bg-transparent px-3"
                value={integration.id}
                disabled={busy}
                onChange={(event) => {
                  setSelected(event.target.value);
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
            <label className="grid gap-2 text-sm font-medium">
              {bt("bloem.admin.bookRequests.ebookSetup.library")}
              <select
                className="border-input h-9 rounded-md border bg-transparent px-3"
                value={library}
                required
                disabled={busy}
                onChange={(event) => setDrafts({ ...drafts, [integration.id]: event.target.value })}
              >
                <option value="">{bt("bloem.admin.bookRequests.ebookSetup.selectLibrary")}</option>
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
              {save.isPending
                ? bt("bloem.admin.bookRequests.ebookSetup.saving")
                : bt("bloem.admin.bookRequests.ebookSetup.saveLibrary")}
            </Button>
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
            {save.isError && <p role="alert">{save.error.message}</p>}
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
            {list.data.libraries.length === 0 && (
              <p role="status" className="text-muted-foreground text-sm">
                {bt("bloem.admin.bookRequests.ebookSetup.noLibraries")}
              </p>
            )}
          </form>
        ) : (
          <p role="status" className="text-muted-foreground text-sm">
            {bt("bloem.admin.bookRequests.ebookSetup.noConnections")}
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
            <h3 className="font-semibold sm:col-span-2">
              {bt("bloem.admin.bookRequests.ebookSetup.addTitle")}
            </h3>
            <p className="text-muted-foreground text-sm sm:col-span-2">
              {bt("bloem.admin.bookRequests.ebookSetup.addHelp")}
            </p>
            <label className="grid gap-2 text-sm font-medium">
              {bt("bloem.admin.bookRequests.ebookSetup.plugin")}
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
              {bt("bloem.admin.bookRequests.connectionName")}
              <Input
                value={name}
                required
                maxLength={255}
                disabled={busy}
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            {!native && (
              <>
                <label className="grid gap-2 text-sm font-medium">
                  {bt("bloem.admin.bookRequests.ebookSetup.baseUrl")}
                  <Input
                    type="url"
                    placeholder={EXAMPLE_BASE_URL}
                    value={baseUrl}
                    required
                    maxLength={2048}
                    disabled={busy}
                    onChange={(event) => setBaseUrl(event.target.value)}
                  />
                </label>
                <label className="grid gap-2 text-sm font-medium">
                  {bt("bloem.admin.bookRequests.ebookSetup.apiKey")}
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
                <label
                  htmlFor={httpOptInId}
                  className="flex items-center gap-2 text-sm sm:col-span-2"
                >
                  <Checkbox
                    id={httpOptInId}
                    checked={allowHttp}
                    disabled={busy}
                    onCheckedChange={(checked) => setAllowHttp(checked === true)}
                  />
                  {bt("bloem.admin.bookRequests.ebookSetup.allowHttp")}
                </label>
              </>
            )}
            <Button type="submit" disabled={busy || !baseUrlAllowed}>
              {create.isPending
                ? bt("bloem.admin.bookRequests.ebookSetup.adding")
                : bt("bloem.admin.bookRequests.ebookSetup.add")}
            </Button>
            {create.isError && <p role="alert">{create.error.message}</p>}
          </form>
        ) : (
          <Button asChild variant="outline">
            <Link to="/admin/plugins">
              {bt("bloem.admin.bookRequests.ebookSetup.installPlugin")}
            </Link>
          </Button>
        ))}
    </section>
  );
}
