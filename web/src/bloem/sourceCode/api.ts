import { useQuery, type QueryClient } from "@tanstack/react-query";

import { nativeApi } from "@/api/bloemClient";
import { ApiClientError, captureSessionIdentity, getAccessToken } from "@/api/client";

/** The capability token a server lists while it offers its source code. */
export const SOURCE_CODE_FEATURE = "source_code_v1";

/** The route segment of the Source code page under /settings. */
export const SOURCE_CODE_SETTINGS_PATH = "source-code";

/** Where the archive is served when the server names no path of its own. */
export const SOURCE_ARCHIVE_PATH = "/api/bloem/v2/source-code/archive";

/** The full text of the license Bloem Server is released under. */
export const AGPL_LICENSE_URL = "https://www.gnu.org/licenses/agpl-3.0.html";

/** `GET /api/bloem/v2/source-code`: the archive of the version this server runs. */
export interface SourceCodeInfo {
  license: string;
  revision: string;
  archive_name: string;
  archive_path: string;
  size_bytes: number;
}

/** Whether `pathname` is the Source code settings page. */
export function isSourceCodeSettingsPath(pathname: string): boolean {
  return pathname.replace(/\/+$/, "") === `/settings/${SOURCE_CODE_SETTINGS_PATH}`;
}

/** The server's Bloem capabilities, under the key BloemRequests.tsx reads them with. */
export function useBloemFeatureTokens() {
  const identity = captureSessionIdentity();
  return useQuery({
    queryKey: ["bloem-capabilities", identity.serverOrigin, identity.authContextVersion],
    queryFn: () => nativeApi<{ feature_tokens: string[] }>("/capabilities"),
  });
}

function sourceCodeQuery() {
  const identity = captureSessionIdentity();
  return {
    queryKey: ["bloem-source-code", identity.serverOrigin, identity.authContextVersion],
    queryFn: () => nativeApi<SourceCodeInfo>("/source-code"),
    // A missing archive stays missing until an operator rebuilds it.
    retry: (failures: number, error: unknown) => !isSourceUnavailable(error) && failures < 2,
  };
}

/** The archive's details, once the server is known to offer them. */
export function useSourceCodeInfo(enabled: boolean) {
  return useQuery({ ...sourceCodeQuery(), enabled });
}

/** The server's answer when it offers its source code but has no archive right now. */
export function isSourceUnavailable(error: unknown): boolean {
  return error instanceof ApiClientError && error.status === 503;
}

/** A short revision id, as `git log --oneline` prints one in this repository. */
export function shortRevision(revision: string): string {
  return revision.slice(0, 9);
}

/**
 * The archive address a plain browser download can open. A download cannot
 * send an Authorization header, so the login token rides in `?token=`, as on
 * media URLs (player/stream-url.ts). Only a path on Bloem's own API gets it: an
 * address anywhere else falls back to the default path rather than hand the
 * token to another origin.
 */
export function sourceArchiveUrl(archivePath: string, token: string | null): string {
  const path =
    archivePath.startsWith("/api/bloem/") && !archivePath.includes("//")
      ? archivePath
      : SOURCE_ARCHIVE_PATH;
  if (!token) return path;
  const separator = path.includes("?") ? "&" : "?";
  return `${path}${separator}${new URLSearchParams({ token }).toString()}`;
}

/**
 * Asks the server for the archive's details again, then returns its download
 * address with the login token current at that moment. The request goes
 * through the session client, which refreshes an expired token and retries,
 * so the token in the address is one the server has just accepted.
 */
export async function prepareSourceDownload(
  queryClient: QueryClient,
): Promise<{ url: string; fileName: string }> {
  const { queryKey, queryFn } = sourceCodeQuery();
  const info = await queryFn();
  queryClient.setQueryData(queryKey, info);
  return {
    url: sourceArchiveUrl(info.archive_path, getAccessToken()),
    fileName: info.archive_name,
  };
}

/**
 * Starts the browser's own download of `url`. The archive is hundreds of
 * megabytes, so it streams to disk rather than through a blob in memory.
 */
export function startBrowserDownload(url: string, fileName: string): void {
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  anchor.style.display = "none";
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
}
