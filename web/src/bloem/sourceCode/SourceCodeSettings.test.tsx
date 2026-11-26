import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Navigate, Route, Routes, useLocation } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiClientError, setAccessToken } from "@/api/client";
import { formatFileSize } from "@/lib/mediaFormat";
import SettingsLayout from "@/pages/SettingsLayout";

import { SOURCE_ARCHIVE_PATH, sourceArchiveUrl, type SourceCodeInfo } from "./api";

const REVISION = "0123456789abcdef0123456789abcdef01234567";
const INFO: SourceCodeInfo = {
  license: "AGPL-3.0",
  revision: REVISION,
  archive_name: `bloem-server-${REVISION}.tar.gz`,
  archive_path: SOURCE_ARCHIVE_PATH,
  size_bytes: 280_000_000,
};

const mocks = vi.hoisted(() => ({
  desktop: false,
  nativeApi: vi.fn(),
}));

vi.mock("@/api/bloemClient", () => ({ nativeApi: mocks.nativeApi }));
// A household member: neither the primary profile nor an administrator.
vi.mock("@/hooks/useCurrentProfile", () => ({
  useCurrentProfile: () => ({
    profile: { id: "viewer", name: "Robin", is_primary: false },
    isLoading: false,
  }),
}));
vi.mock("@/hooks/useIsActingAdmin", () => ({ useIsActingAdmin: () => false }));
vi.mock("@/hooks/queries/useRequests", () => ({
  useRequestFeatureStatus: () => ({ data: { requests_enabled: false } }),
}));
vi.mock("@/hooks/useMediaQuery", () => ({ useMediaQuery: () => mocks.desktop }));

/** Answers the two native reads; `source` decides the /source-code answer. */
function serve(features: string[], source: () => Promise<SourceCodeInfo> = async () => INFO): void {
  mocks.nativeApi.mockImplementation(async (path: string) => {
    if (path === "/capabilities") return { feature_tokens: features };
    if (path === "/source-code") return source();
    throw new Error(`unexpected ${path}`);
  });
}

function Where() {
  return <output data-testid="location">{useLocation().pathname}</output>;
}

/** The personal settings routes as App.tsx declares them, `*` redirect included. */
function renderSettings(path: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/settings/*" element={<SettingsLayout />}>
            <Route index element={null} />
            <Route path="playback" element={<h1>Playback page</h1>} />
            <Route path="*" element={<Navigate to="/settings/playback" replace />} />
          </Route>
        </Routes>
        <Where />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mocks.desktop = false;
  mocks.nativeApi.mockReset();
  setAccessToken("old-token");
});

afterEach(() => {
  setAccessToken(null);
  vi.restoreAllMocks();
});

describe("Settings → Source code entry", () => {
  it("is listed last, under About, for a profile that is neither primary nor admin", () => {
    serve(["source_code_v1"]);
    renderSettings("/settings");

    const link = screen.getByRole("link", { name: /Source code/ });
    expect(link.getAttribute("href")).toBe("/settings/source-code");
    // The primary-only pages are hidden, so this really is a household member.
    expect(screen.queryByRole("link", { name: /Profiles/ })).toBeNull();
    const about = screen.getByText("About");
    expect(about.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const links = screen
      .getAllByRole("link")
      .filter((a) => a.getAttribute("href")?.startsWith("/settings/"));
    expect(links.at(-1)).toBe(link);
  });

  it.each(["agpl", "licence", "license", "open source", "download"])(
    "is found by searching for %s",
    (query) => {
      serve(["source_code_v1"]);
      renderSettings("/settings");

      fireEvent.change(screen.getByRole("searchbox"), { target: { value: query } });

      expect(screen.getByRole("link", { name: /Source code/ })).toBeTruthy();
    },
  );

  it("leaves the desktop index opening the first Silo page", async () => {
    mocks.desktop = true;
    serve(["source_code_v1"]);
    renderSettings("/settings");

    expect(await screen.findByRole("heading", { name: "Playback page" })).toBeTruthy();
    expect(screen.getByTestId("location").textContent).toBe("/settings/playback");
  });
});

describe("Settings → Source code page", () => {
  it("renders inside the settings layout instead of the playback redirect", async () => {
    serve(["source_code_v1"]);
    renderSettings("/settings/source-code");

    expect(await screen.findByRole("heading", { level: 1, name: "Source code" })).toBeTruthy();
    // The settings frame's phone back link around the page.
    expect(screen.getByRole("link", { name: /All settings/ }).getAttribute("href")).toBe(
      "/settings",
    );
    expect(screen.queryByRole("heading", { name: "Playback page" })).toBeNull();
    expect(screen.getByTestId("location").textContent).toBe("/settings/source-code");
    expect(document.title).toMatch(/^Source code · /);
    expect(
      screen.getByText(/runs Bloem Server, free software released under the GNU Affero/),
    ).toBeTruthy();
    const license = screen.getByRole("link", { name: /GNU Affero General Public License/ });
    expect(license.getAttribute("href")).toBe("https://www.gnu.org/licenses/agpl-3.0.html");
    expect(license.getAttribute("target")).toBe("_blank");

    expect(await screen.findByText("012345678")).toBeTruthy();
    expect(screen.getByText(formatFileSize(INFO.size_bytes))).toBeTruthy();
    expect(screen.getByRole("button", { name: "Download source code" })).toBeTruthy();
  });

  it("is the page on desktop too", async () => {
    mocks.desktop = true;
    serve(["source_code_v1"]);
    renderSettings("/settings/source-code");

    expect(await screen.findByRole("button", { name: "Download source code" })).toBeTruthy();
    expect(screen.getByTestId("location").textContent).toBe("/settings/source-code");
  });

  it("downloads the archive path with the login token current at the click", async () => {
    let calls = 0;
    serve(["source_code_v1"], async () => {
      calls += 1;
      // The click's request is the one that met a 401 and refreshed the token.
      if (calls > 1) setAccessToken("fresh-token");
      return INFO;
    });
    const opened: { href: string; download: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      opened.push({ href: this.getAttribute("href") ?? "", download: this.download });
    });
    renderSettings("/settings/source-code");

    fireEvent.click(await screen.findByRole("button", { name: "Download source code" }));

    await waitFor(() => expect(opened).toHaveLength(1));
    expect(calls).toBe(2);
    expect(opened[0]).toEqual({
      href: "/api/bloem/v2/source-code/archive?token=fresh-token",
      download: INFO.archive_name,
    });
    // The archive streams to disk; nothing reads it into the page.
    expect(mocks.nativeApi).not.toHaveBeenCalledWith(
      expect.stringContaining("/archive"),
      expect.anything(),
    );
  });

  it("says the download is not available right now when the archive is missing", async () => {
    serve(["source_code_v1"], async () => {
      throw new ApiClientError(503, "source_unavailable", "The archive is missing.");
    });
    renderSettings("/settings/source-code");

    expect(
      await screen.findByText(
        "The source code download is not available right now. Try again later.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Download source code" })).toBeNull();
  });

  it("says when the archive goes missing between loading the page and the click", async () => {
    let calls = 0;
    serve(["source_code_v1"], async () => {
      calls += 1;
      if (calls > 1) throw new ApiClientError(503, "source_unavailable", "The archive is missing.");
      return INFO;
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    renderSettings("/settings/source-code");

    fireEvent.click(await screen.findByRole("button", { name: "Download source code" }));

    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "The source code download is not available right now. Try again later.",
    );
    expect(click).not.toHaveBeenCalled();
  });

  it("says the server offers no download without source_code_v1", async () => {
    serve(["audiobook_requests_v1"]);
    renderSettings("/settings/source-code");

    expect(
      await screen.findByText("This server does not offer a source code download."),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Download source code" })).toBeNull();
    expect(mocks.nativeApi).not.toHaveBeenCalledWith("/source-code");
    // The license statement stays.
    expect(screen.getByRole("link", { name: /GNU Affero General Public License/ })).toBeTruthy();
  });

  it("offers a retry when the capability check fails", async () => {
    mocks.nativeApi.mockRejectedValue(new Error("offline"));
    renderSettings("/settings/source-code");

    const group = (
      await screen.findByText(
        "The app could not check whether this server offers a source code download.",
      )
    ).closest("section");
    expect(group).not.toBeNull();
    expect(within(group as HTMLElement).getByRole("button", { name: "Try again" })).toBeTruthy();
  });
});

describe("sourceArchiveUrl", () => {
  it("appends the token to the server's archive path", () => {
    expect(sourceArchiveUrl("/api/bloem/v2/source-code/archive", "a b")).toBe(
      "/api/bloem/v2/source-code/archive?token=a+b",
    );
    expect(sourceArchiveUrl("/api/bloem/v2/source-code/archive?rev=1", "t")).toBe(
      "/api/bloem/v2/source-code/archive?rev=1&token=t",
    );
  });

  it("never sends the token to another origin", () => {
    for (const path of ["https://elsewhere.example/a.tgz", "//elsewhere.example/a", "/other"]) {
      expect(sourceArchiveUrl(path, "t")).toBe(`${SOURCE_ARCHIVE_PATH}?token=t`);
    }
  });
});
