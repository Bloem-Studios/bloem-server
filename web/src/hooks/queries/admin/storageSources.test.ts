import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AdminV2ClientError } from "@/api/adminV2Client";
import {
  discoverBody,
  isAllBucketsSource,
  locationRootName,
  rotateBody,
  s3BucketsBody,
  s3FoldersBody,
  storageSourceLabel,
  useConfigureStorageSource,
  useDiscoverStorageSource,
  useDisableStorageSource,
  useInstallStorageSource,
  useListS3Buckets,
  useListS3Folders,
  useRemoveStorageSource,
  useReplaceStorageConfiguration,
  useRotateStorageCredentials,
  useTestStorageSource,
  type StorageSource,
} from "./storageSources";

const adminV2Api = vi.hoisted(() => vi.fn());
const adminContext = vi.hoisted(() => ({ scope: "platform" as "platform" | "organization" }));

vi.mock("@/api/adminV2Client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/adminV2Client")>()),
  adminV2Api,
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => ({
    active: { scope: adminContext.scope, key: adminContext.scope },
  }),
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

describe("S3 browse request bodies", () => {
  const connection = {
    endpoint: "https://s3.example.com",
    region: "us-east-1",
    accessKey: "AK",
    secretKey: "SK",
  };
  it("buckets sends snake_case connection fields and omits an empty session token", () => {
    expect(s3BucketsBody({ ...connection, sessionToken: "" })).toEqual({
      endpoint: "https://s3.example.com",
      region: "us-east-1",
      access_key: "AK",
      secret_key: "SK",
    });
    expect(s3BucketsBody({ ...connection, sessionToken: "   " })).not.toHaveProperty(
      "session_token",
    );
    expect(s3BucketsBody(connection)).not.toHaveProperty("session_token");
    expect(s3BucketsBody({ ...connection, sessionToken: "T" })).toHaveProperty(
      "session_token",
      "T",
    );
  });
  it("folders adds the bucket and only a non-empty prefix and cursor", () => {
    expect(s3FoldersBody({ ...connection, bucket: "media" })).toEqual({
      endpoint: "https://s3.example.com",
      region: "us-east-1",
      access_key: "AK",
      secret_key: "SK",
      bucket: "media",
    });
    expect(
      s3FoldersBody({ ...connection, bucket: "media", prefix: "", cursor: "", sessionToken: "" }),
    ).toEqual(s3FoldersBody({ ...connection, bucket: "media" }));
    expect(
      s3FoldersBody({ ...connection, bucket: "media", prefix: "movies/", cursor: "c1" }),
    ).toMatchObject({ bucket: "media", prefix: "movies/", cursor: "c1" });
  });
});

describe("storage source mutations", () => {
  beforeEach(() => {
    adminV2Api.mockReset();
    adminContext.scope = "platform";
  });

  it("discover raises no toast when the caller reports the outcome itself", async () => {
    const { toast } = await import("sonner");
    vi.mocked(toast.success).mockClear();
    vi.mocked(toast.error).mockClear();
    adminV2Api.mockResolvedValue({ source });
    const quiet = renderHook(() => useDiscoverStorageSource(), { wrapper });
    await quiet.result.current.mutateAsync({
      artifactKey: "a",
      providerSourceId: "p",
      config: {},
      silent: true,
    });
    adminV2Api.mockRejectedValue(new AdminV2ClientError(500, "internal", "raw server text"));
    await expect(
      quiet.result.current.mutateAsync({
        artifactKey: "a",
        providerSourceId: "p",
        config: {},
        silent: true,
      }),
    ).rejects.toBeInstanceOf(AdminV2ClientError);
    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.error).not.toHaveBeenCalled();
    // Without the flag, the existing behaviour is unchanged.
    adminV2Api.mockResolvedValue({ source });
    await quiet.result.current.mutateAsync({ artifactKey: "a", providerSourceId: "p", config: {} });
    expect(toast.success).toHaveBeenCalledWith("Storage source added");
  });

  it("lists buckets from the active scope and returns only the names", async () => {
    adminV2Api.mockResolvedValue({ buckets: [{ name: "movies" }, { name: "television" }] });
    const { result } = renderHook(() => useListS3Buckets(), { wrapper });
    const names = await result.current.mutateAsync({
      endpoint: "https://s3.example.com",
      region: "us-east-1",
      accessKey: "AK",
      secretKey: "SK",
    });
    expect(names).toEqual(["movies", "television"]);
    const [path, init] = adminV2Api.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/platform/native-storage/s3/buckets");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      endpoint: "https://s3.example.com",
      region: "us-east-1",
      access_key: "AK",
      secret_key: "SK",
    });
  });

  it("builds the browse URLs from the organization scope too", async () => {
    adminContext.scope = "organization";
    adminV2Api.mockResolvedValue({ buckets: [], prefix: "", folders: [], next_cursor: "" });
    const buckets = renderHook(() => useListS3Buckets(), { wrapper });
    await buckets.result.current.mutateAsync({
      endpoint: "https://s3.example.com",
      region: "r",
      accessKey: "AK",
      secretKey: "SK",
    });
    const folders = renderHook(() => useListS3Folders(), { wrapper });
    await folders.result.current.mutateAsync({
      endpoint: "https://s3.example.com",
      region: "r",
      accessKey: "AK",
      secretKey: "SK",
      bucket: "media",
    });
    expect(adminV2Api.mock.calls[0]?.[0]).toBe("/organization/native-storage/s3/buckets");
    expect(adminV2Api.mock.calls[1]?.[0]).toBe("/organization/native-storage/s3/folders");
  });

  it("lists one level of folders and maps the cursor to camelCase", async () => {
    adminV2Api.mockResolvedValue({
      prefix: "movies/",
      folders: [{ name: "10s", prefix: "movies/10s/" }],
      next_cursor: "more",
    });
    const { result } = renderHook(() => useListS3Folders(), { wrapper });
    const page = await result.current.mutateAsync({
      endpoint: "https://s3.example.com",
      region: "us-east-1",
      accessKey: "AK",
      secretKey: "SK",
      bucket: "media",
      prefix: "movies/",
    });
    expect(page).toEqual({
      prefix: "movies/",
      folders: [{ name: "10s", prefix: "movies/10s/" }],
      nextCursor: "more",
    });
    const [path, init] = adminV2Api.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/platform/native-storage/s3/folders");
    expect(JSON.parse(String(init.body))).toMatchObject({ bucket: "media", prefix: "movies/" });
  });

  it("tolerates a response without the optional parts", async () => {
    adminV2Api.mockResolvedValue({});
    const buckets = renderHook(() => useListS3Buckets(), { wrapper });
    await expect(
      buckets.result.current.mutateAsync({
        endpoint: "https://e",
        region: "r",
        accessKey: "a",
        secretKey: "s",
      }),
    ).resolves.toEqual([]);
    const folders = renderHook(() => useListS3Folders(), { wrapper });
    await expect(
      folders.result.current.mutateAsync({
        endpoint: "https://e",
        region: "r",
        accessKey: "a",
        secretKey: "s",
        bucket: "b",
      }),
    ).resolves.toEqual({ prefix: "", folders: [], nextCursor: "" });
  });

  it.each([
    ["buckets", () => useListS3Buckets()],
    ["folders", () => useListS3Folders()],
  ] as const)("%s keeps the server error code and status and raises no toast", async (_n, use) => {
    adminV2Api.mockImplementation(async () => {
      throw new AdminV2ClientError(422, "s3_access_denied", "The S3 backend denied access");
    });
    const { result } = renderHook(
      () =>
        use() as unknown as {
          mutate: (v: unknown) => void;
          isError: boolean;
          error: unknown;
        },
      { wrapper },
    );
    result.current.mutate({
      endpoint: "https://e",
      region: "r",
      accessKey: "a",
      secretKey: "s",
      bucket: "b",
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeInstanceOf(AdminV2ClientError);
    expect((result.current.error as AdminV2ClientError).code).toBe("s3_access_denied");
    expect((result.current.error as AdminV2ClientError).status).toBe(422);
    const { toast } = await import("sonner");
    expect(toast.error).not.toHaveBeenCalled();
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
      "list S3 buckets",
      () => useListS3Buckets(),
      {
        endpoint: "https://e",
        region: "r",
        accessKey: SECRET,
        secretKey: SECRET,
        sessionToken: SECRET,
      },
    ],
    [
      "list S3 folders",
      () => useListS3Folders(),
      {
        endpoint: "https://e",
        region: "r",
        accessKey: SECRET,
        secretKey: SECRET,
        sessionToken: SECRET,
        bucket: "b",
      },
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

describe("storage source names", () => {
  const s3 = (summary: Record<string, string>) =>
    ({
      plugin_id: "bloem.storage.s3",
      provider_source_id: "s3_abc",
      root_entry_id: "root",
      connection_summary: summary,
    }) as unknown as StorageSource;

  it("names an S3 source by its source name, and says when it covers every bucket", () => {
    expect(storageSourceLabel(s3({ source_name: "Movies", bucket: "media", region: "x" }))).toBe(
      "Movies",
    );
    const all = s3({ source_name: "Backblaze B2", region: "us-west-004", prefix: "" });
    expect(isAllBucketsSource(all)).toBe(true);
    expect(storageSourceLabel(all)).toBe("Backblaze B2 · all buckets");
  });

  it("falls back to the source id and keeps other plugins' names", () => {
    expect(storageSourceLabel(s3({}))).toBe("s3_abc");
    expect(isAllBucketsSource(s3({}))).toBe(false);
    expect(isAllBucketsSource(s3({ prefix: "" }))).toBe(false);
    expect(
      storageSourceLabel({
        plugin_id: "bloem.storage.bookwarehouse",
        root_entry_id: "audio-root",
      } as unknown as StorageSource),
    ).toBe("Bookwarehouse audiobooks");
  });
});

describe("locationRootName", () => {
  const all = {
    plugin_id: "bloem.storage.s3",
    provider_source_id: "s3_all",
    connection_summary: { source_name: "Backblaze B2", region: "us-west-004" },
  } as unknown as StorageSource;

  it("calls the root of an all-buckets source All buckets", () => {
    expect(locationRootName({ name: "s3_all", logical_path: "" }, all)).toBe("All buckets");
    expect(locationRootName({ name: "movies", logical_path: "movies/" }, all)).toBe("movies");
  });

  it("keeps other names, and Root for a nameless root", () => {
    expect(locationRootName({ name: "", logical_path: "" }, undefined)).toBe("Root");
    expect(locationRootName({ name: "Books", logical_path: "" }, undefined)).toBe("Books");
  });
});
