// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

import { adminV2Api, AdminV2ClientError } from "@/api/adminV2Client";
import CompatibilityApplicationsPage from "./CompatibilityApplicationsPage";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

const jellyfinApplication = {
  instance_id: "inst-jelly-1",
  kind: "jellyfin",
  state: "enabled",
  enabled: true,
  revoked: false,
  healthy: true,
  version: "1.4.2",
  image_digest: "sha256:abcdef0123",
  api_range: { min: "1.0", max: "1.3" },
  last_contact_at: "2026-08-14T10:00:00Z",
  active_sessions: 3,
  capabilities: ["identity.exchange", "catalog.read"],
  revision: 7,
  canonical_url: "https://bloem.example/",
  commands: {
    install: "docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml up -d",
    update:
      "docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml pull bloem-jellyfin && docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml up -d bloem-jellyfin",
    rollback:
      "Pin the previous image digest for bloem-jellyfin in docker-compose.jellyfin.yml, then run: docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml up -d bloem-jellyfin",
    remove:
      "docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml rm -sf bloem-jellyfin",
  },
};

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={["/admin/platform/compatibility"]}>
        <CompatibilityApplicationsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe("CompatibilityApplicationsPage", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("shows lifecycle state, identity, and exact operator commands without touching Docker", async () => {
    vi.mocked(adminV2Api).mockResolvedValue({
      applications: [jellyfinApplication],
    } as never);

    renderPage();

    expect(await screen.findByText("inst-jelly-1")).toBeInTheDocument();
    expect(screen.getByText("1.4.2")).toBeInTheDocument();
    expect(screen.getByText("sha256:abcdef0123")).toBeInTheDocument();
    expect(screen.getByText("1.0 – 1.3")).toBeInTheDocument();
    expect(screen.getByText("https://bloem.example/")).toBeInTheDocument();
    expect(
      screen.getByText("docker compose -f docker-compose.yml -f docker-compose.jellyfin.yml up -d"),
    ).toBeInTheDocument();

    // The administration surface reports and instructs; it never mutates
    // containers. Every request goes to the Bloem admin API and rendering
    // performs no mutation at all.
    const calls = vi.mocked(adminV2Api).mock.calls;
    expect(calls.length).toBeGreaterThan(0);
    for (const [path, init] of calls) {
      expect(String(path).startsWith("/platform/compatibility")).toBe(true);
      expect(init?.method ?? "GET").toBe("GET");
    }
  });

  it("creates an enrollment and reveals the one-time secret", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (init?.method === "POST") {
        expect(String(path)).toBe("/platform/compatibility/enrollments");
        expect(JSON.parse(String(init.body))).toEqual({
          kind: "audiobookshelf",
          capabilities: [],
        });
        return {
          enrollment: {
            kind: "audiobookshelf",
            secret: "vce_one_time_secret",
            expires_at: "2026-08-14T11:00:00Z",
          },
        } as never;
      }
      return { applications: [] } as never;
    });

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "New enrollment" }));
    fireEvent.change(screen.getByLabelText("Application"), {
      target: { value: "audiobookshelf" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create enrollment" }));

    expect(await screen.findByText("vce_one_time_secret")).toBeInTheDocument();
    expect(screen.getByText(/shown only once/i)).toBeInTheDocument();
  });

  it("guards mutations with the expected revision and surfaces conflicts", async () => {
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (init?.method === "POST") {
        expect(String(path)).toBe("/platform/compatibility/applications/inst-jelly-1/disable");
        expect(JSON.parse(String(init.body))).toEqual({ expected_revision: 7 });
        throw new AdminV2ClientError(
          409,
          "authorization_state_changed",
          "Application state changed; reload and retry",
        );
      }
      return { applications: [jellyfinApplication] } as never;
    });

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Disable" }));

    expect(
      await screen.findByText("Application state changed; reload and retry"),
    ).toBeInTheDocument();
  });

  it("requires explicit confirmation before revoking", async () => {
    const revoke = vi.fn();
    vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
      if (init?.method === "POST") {
        revoke(String(path), JSON.parse(String(init.body)));
        return { application: { ...jellyfinApplication, state: "revoked" } } as never;
      }
      return { applications: [jellyfinApplication] } as never;
    });

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Revoke" }));
    expect(revoke).not.toHaveBeenCalled();

    fireEvent.click(await screen.findByRole("button", { name: "Revoke credentials" }));
    await waitFor(() =>
      expect(revoke).toHaveBeenCalledWith(
        "/platform/compatibility/applications/inst-jelly-1/revoke",
        { expected_revision: 7, confirmed: true },
      ),
    );
  });
});
