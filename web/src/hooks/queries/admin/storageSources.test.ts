import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError } from "@/api/adminV2Client";
import {
  discoverBody,
  rotateBody,
  useConfigureStorageSource,
  useDiscoverStorageSource,
  useDisableStorageSource,
  useInstallStorageSource,
  useRemoveStorageSource,
  useReplaceStorageConfiguration,
  useRotateStorageCredentials,
  useTestStorageSource,
  type StorageSource,
} from "./storageSources";

const adminV2Api = vi.hoisted(() => vi.fn());

vi.mock("@/api/adminV2Client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/adminV2Client")>()),
  adminV2Api,
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => ({ active: { scope: "platform", key: "platform" } }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const source: StorageSource = {
  source_key: "plat:s3_abc",
  owner_kind: "platform",
  installation_id: 12,
  plugin_id: "bloem.storage.s3",
  provider_source_id: "s3_abc",
  root_entry_id: "root",
  configuration_revision: 4,
  enabled: true,
  state: "ready",
  configured: true,
  connection_summary: { bucket: "media" },
};

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  return createElement(QueryClientProvider, { client }, children);
}

describe("storage source request bodies", () => {
  it("discover sends the catalog contract and no state path or owner", () => {
    const body = discoverBody({
      artifactKey: "s3-0.3.2-linux-amd64-x",
      providerSourceId: "s3_abc",
      config: { connection: { source_id: "s3_abc", bucket: "media" } },
    });
    expect(body).toEqual({
      artifact_key: "s3-0.3.2-linux-amd64-x",
      provider_source_id: "s3_abc",
      enabled: true,
      config: { connection: { source_id: "s3_abc", bucket: "media" } },
    });
  });
  it("rotate sends only the revision and credentials", () => {
    expect(rotateBody({ revision: 4, accessKey: "AK", secretKey: "SK", sessionToken: "" })).toEqual(
      {
        expected_revision: 4,
        access_key: "AK",
        secret_key: "SK",
      },
    );
    expect(
      rotateBody({ revision: 4, accessKey: "AK", secretKey: "SK", sessionToken: "T" }),
    ).toHaveProperty("session_token", "T");
  });
});

describe("storage source mutations", () => {
  beforeEach(() => {
    adminV2Api.mockReset();
  });

  it("replaces configuration with the loaded revision", async () => {
    adminV2Api.mockResolvedValue({ source });
    const { result } = renderHook(() => useReplaceStorageConfiguration(), { wrapper });
    result.current.mutate({ source, config: { connection: { bucket: "b" } } });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const [path, init] = adminV2Api.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/platform/native-storage/sources/plat%3As3_abc/configuration");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({
      expected_revision: 4,
      config: { connection: { bucket: "b" } },
    });
  });

  it("rotates credentials without a session token when none is given", async () => {
    adminV2Api.mockResolvedValue({ source });
    const { result } = renderHook(() => useRotateStorageCredentials(), { wrapper });
    result.current.mutate({ source, accessKey: "AK", secretKey: "SK" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const [path, init] = adminV2Api.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/platform/native-storage/sources/plat%3As3_abc/credentials");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      expected_revision: 4,
      access_key: "AK",
      secret_key: "SK",
    });
  });

  it("disables and removes through the installation with the revision guard", async () => {
    adminV2Api.mockResolvedValue(undefined);
    const disable = renderHook(() => useDisableStorageSource(), { wrapper });
    disable.result.current.mutate({ source });
    await waitFor(() => expect(disable.result.current.isSuccess).toBe(true));
    const remove = renderHook(() => useRemoveStorageSource(), { wrapper });
    remove.result.current.mutate({ source });
    await waitFor(() => expect(remove.result.current.isSuccess).toBe(true));

    const [disablePath, disableInit] = adminV2Api.mock.calls[0] as [string, RequestInit];
    expect(disablePath).toBe("/platform/native-storage/installations/12/disable");
    expect(disableInit.method).toBe("POST");
    const [removePath, removeInit] = adminV2Api.mock.calls[1] as [string, RequestInit];
    expect(removePath).toBe("/platform/native-storage/installations/12");
    expect(removeInit.method).toBe("DELETE");
    for (const init of [disableInit, removeInit]) {
      expect(JSON.parse(String(init.body))).toEqual({
        source_key: "plat:s3_abc",
        expected_revision: 4,
      });
    }
  });

  it("surfaces the server error code on a failed mutation", async () => {
    adminV2Api.mockImplementation(async () => {
      throw new AdminV2ClientError(
        409,
        "configuration_namespace_unverified",
        "Namespace unverified",
      );
    });
    const { result } = renderHook(() => useReplaceStorageConfiguration(), { wrapper });
    result.current.mutate({ source, config: {} });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeInstanceOf(AdminV2ClientError);
    expect((result.current.error as AdminV2ClientError).code).toBe(
      "configuration_namespace_unverified",
    );
  });

  it("test connection lists one entry and resolves ok", async () => {
    adminV2Api.mockResolvedValue({ entries: [] });
    const { result } = renderHook(() => useTestStorageSource(), { wrapper });
    const outcome = await result.current.mutateAsync({ source });
    expect(outcome).toEqual({ ok: true, message: "Connection works" });
    expect(adminV2Api.mock.calls[0]?.[0]).toBe(
      "/platform/native-storage/sources/plat%3As3_abc/browse?limit=1",
    );
  });

  it("test connection never throws: a failure resolves ok false with the message", async () => {
    adminV2Api.mockImplementation(async () => {
      throw new AdminV2ClientError(502, "storage_unreachable", "Bucket unreachable");
    });
    const { result } = renderHook(() => useTestStorageSource(), { wrapper });
    await expect(result.current.mutateAsync({ source })).resolves.toEqual({
      ok: false,
      message: "Bucket unreachable",
    });
  });
});

describe("storage source mutations holding credentials", () => {
  beforeEach(() => {
    adminV2Api.mockReset();
    adminV2Api.mockResolvedValue({ source });
  });

  const SECRET = "distinctive-secret-value-7f3a";
  const cases: [string, () => unknown, unknown][] = [
    [
      "install",
      () => useInstallStorageSource(),
      {
        artifactKey: "a",
        providerSourceId: "p",
        rootEntryId: "r",
        config: { connection: { secret_key: SECRET } },
      },
    ],
    [
      "discover",
      () => useDiscoverStorageSource(),
      { artifactKey: "a", providerSourceId: "p", config: { connection: { secret_key: SECRET } } },
    ],
    [
      "replace configuration",
      () => useReplaceStorageConfiguration(),
      { source, config: { connection: { secret_key: SECRET } } },
    ],
    [
      "rotate credentials",
      () => useRotateStorageCredentials(),
      { source, accessKey: "AK", secretKey: SECRET, sessionToken: SECRET },
    ],
    [
      "configure bookwarehouse",
      () => useConfigureStorageSource(),
      { source, url: "https://x.invalid", apiKey: SECRET },
    ],
  ];

  it.each(cases)(
    "%s leaves no secret in the mutation cache once settled",
    async (_name, useHook, input) => {
      const client = new QueryClient();
      const clientWrapper = ({ children }: { children: ReactNode }) =>
        createElement(QueryClientProvider, { client }, children);
      const { result, unmount } = renderHook(
        () =>
          useHook() as {
            mutate: (v: unknown) => void;
            isSuccess: boolean;
          },
        { wrapper: clientWrapper },
      );
      result.current.mutate(input);
      await waitFor(() => expect(result.current.isSuccess).toBe(true));
      unmount();
      await waitFor(() => {
        const retained = client
          .getMutationCache()
          .getAll()
          .filter((mutation) => JSON.stringify(mutation.state.variables ?? null).includes(SECRET));
        expect(retained).toEqual([]);
      });
    },
  );

  it("rotate omits a whitespace-only session token", () => {
    expect(
      rotateBody({ revision: 1, accessKey: "AK", secretKey: "SK", sessionToken: "   " }),
    ).not.toHaveProperty("session_token");
  });
});
