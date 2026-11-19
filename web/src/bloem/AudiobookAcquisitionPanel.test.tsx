import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { AdminContextSummary } from "@/api/bloemTypes";
import {
  AdminV2ClientError,
  activateAdminV2Context,
  clearAdminV2Context,
} from "@/api/adminV2Client";
import { setAccessToken } from "@/api/client";
const mocks = vi.hoisted(() => ({
  setup: vi.fn(),
  list: vi.fn(),
  detail: vi.fn(),
  createIntegration: vi.fn(),
  updateIntegration: vi.fn(),
  testIntegration: vi.fn(),
  setLibrary: vi.fn(),
  approveCandidate: vi.fn(),
  acceptDownload: vi.fn(),
  retry: vi.fn(),
  active: null as AdminContextSummary | null,
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({ active: mocks.active, switching: false }),
}));
vi.mock("@/api/bloemAudiobookAcquisitions", () => ({ audiobookAcquisitionsApi: mocks }));
import { AudiobookAcquisitionPanel } from "./AudiobookAcquisitionPanel";
const org = (
  name: string,
  authority: "platform_admin" | "organization_admin" = "platform_admin",
): AdminContextSummary => ({
  key: `organization:${name}`,
  organizationId: name,
  scope: "organization",
  name,
  status: "active",
  authority,
  policyRevision: 1,
  securityRevision: 1,
});
const setup = {
  available: true,
  integrations: [],
  libraries: [{ id: 3, name: "Audio", language: "en", paths: ["/synthetic/audio"] }],
  installations: [{ id: 4, name: "Audiobook requests", capability_id: "audiobooks" }],
};
function row(attempt = 2) {
  return {
    request: {
      id: "request-a",
      title: "Synthetic book",
      author: "An author",
      status: "approved",
      outcome: "active",
      revision: 7,
      fulfilment: "review",
    },
    attempt,
    dispatched: true,
    provider_status: "review",
    external_status: "",
    review_kind: "selection",
    search_passes: 1,
    failure_count: 0,
    ai_calls: 0,
    stuck: false,
    decisions: [
      {
        id: 5,
        attempt: 1,
        phase: "select",
        outcome: "held",
        decision: {
          candidates: [
            { ref: "abook:1", title: "Old candidate", source: "abook", size_mb: 400, h: 0.8 },
          ],
        },
      },
      {
        id: 4,
        attempt: 2,
        phase: "select",
        outcome: "held",
        decision: {
          candidates: [
            { ref: "abook:2", title: "Current candidate", source: "abook", size_mb: 500, h: 0.9 },
          ],
          heuristic: { h: 0.9, reason: "Title and author match" },
        },
      },
    ],
  };
}
function open() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const ui = () => (
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AudiobookAcquisitionPanel />
      </MemoryRouter>
    </QueryClientProvider>
  );
  const view = render(ui());
  return { ...view, updateScope: () => view.rerender(ui()) };
}
beforeEach(() => {
  vi.clearAllMocks();
  setAccessToken("synthetic-account");
  mocks.active = org("a");
  activateAdminV2Context("synthetic-admin", "organization:a");
  mocks.setup.mockResolvedValue(setup);
  mocks.list.mockResolvedValue({ acquisitions: [row()] });
  mocks.detail.mockResolvedValue(row());
});
afterEach(() => clearAdminV2Context());
it("shows unconfigured/read-only setup without offering organization admins credential entry", async () => {
  mocks.active = org("a", "organization_admin");
  open();
  expect(await screen.findByText("No audiobook connections are configured.")).toBeInTheDocument();
  expect(screen.queryByLabelText("qBittorrent password")).not.toBeInTheDocument();
  expect(screen.getByText(/platform admin/)).toBeInTheDocument();
});
it("only approves exact candidates from the current attempt's newest held selection", async () => {
  mocks.approveCandidate.mockResolvedValue({});
  open();
  fireEvent.click(
    await screen.findByRole("button", { name: /Review acquisition.*Synthetic book/ }),
  );
  expect(await screen.findByText("Current candidate")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Approve.*Old candidate/ })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Approve candidate Current candidate" }));
  await waitFor(() =>
    expect(mocks.approveCandidate).toHaveBeenCalledWith(
      "request-a",
      7,
      "abook:2",
      expect.objectContaining({ key: "organization:a" }),
    ),
  );
});
it("does not replay unknown connection creation and clears submitted secret drafts", async () => {
  mocks.createIntegration.mockRejectedValue(new TypeError("Unknown outcome"));
  open();
  const input = await screen.findByLabelText("qBittorrent password");
  fireEvent.change(input, { target: { value: "synthetic-password" } });
  fireEvent.change(screen.getByLabelText("Connection name"), {
    target: { value: "Synthetic connection" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add audiobook connection" }));
  expect(await screen.findByText(/Connection outcome is unknown/)).toBeInTheDocument();
  expect(mocks.createIntegration).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("qBittorrent password")).toHaveValue("");
  expect(screen.getByRole("button", { name: "Add audiobook connection" })).toBeDisabled();
});
it("clears secret drafts and ignores a late detail reply after context switch", async () => {
  let release!: (value: unknown) => void;
  mocks.detail.mockImplementation(
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  const view = open();
  fireEvent.change(await screen.findByLabelText("qBittorrent password"), {
    target: { value: "synthetic-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Review acquisition.*Synthetic book/ }));
  await waitFor(() => expect(mocks.detail).toHaveBeenCalled());
  mocks.active = org("b");
  activateAdminV2Context("other-admin", "organization:b");
  view.updateScope();
  await act(async () => {
    release(row());
  });
  expect(await screen.findByLabelText("qBittorrent password")).toHaveValue("");
  expect(screen.queryByText("Current candidate")).not.toBeInTheDocument();
  expect(mocks.approveCandidate).not.toHaveBeenCalled();
});
it("sends host state filters and does not accept verification with multiple items", async () => {
  const detail = {
    ...row(),
    review_kind: "verification",
    decisions: [
      {
        id: 6,
        attempt: 2,
        phase: "verify",
        outcome: "review",
        decision: { issues: ["multiple_items"] },
      },
    ],
  };
  mocks.detail.mockResolvedValue(detail);
  open();
  fireEvent.change(await screen.findByLabelText("Acquisition state"), {
    target: { value: "stuck" },
  });
  await waitFor(() =>
    expect(mocks.list).toHaveBeenCalledWith(expect.anything(), "stuck", "", expect.anything()),
  );
  fireEvent.click(screen.getByRole("button", { name: /Review acquisition.*Synthetic book/ }));
  expect(await screen.findByText(/Multiple library items/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Accept downloaded audio" })).not.toBeInTheDocument();
});
it("requires explicit download roots instead of inferring a library path", async () => {
  mocks.setup.mockResolvedValue({
    ...setup,
    integrations: [
      {
        id: "connection-a",
        name: "Audio delivery",
        installation_id: 4,
        capability_id: "audiobooks",
        library_id: 0,
        download_roots: [],
        binding_revision: 1,
        ready: false,
      },
    ],
  });
  mocks.setLibrary.mockResolvedValue({});
  open();
  fireEvent.change(await screen.findByLabelText("Audiobook library"), { target: { value: "3" } });
  expect(screen.getByLabelText(/Download roots \(one per line\)/)).toHaveValue("");
  expect(screen.getByRole("button", { name: "Save audiobook library and roots" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText(/Download roots \(one per line\)/), {
    target: { value: " /synthetic/audio/incoming\n/synthetic/audio/ready " },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save audiobook library and roots" }));
  await waitFor(() =>
    expect(mocks.setLibrary).toHaveBeenCalledWith(
      "connection-a",
      3,
      ["/synthetic/audio/incoming", "/synthetic/audio/ready"],
      expect.objectContaining({ key: "organization:a" }),
    ),
  );
});
it("blocks an unknown action until a successful reload and uses the refreshed revision", async () => {
  mocks.approveCandidate
    .mockRejectedValueOnce(new TypeError("Unknown outcome"))
    .mockResolvedValue({});
  open();
  fireEvent.click(
    await screen.findByRole("button", { name: /Review acquisition.*Synthetic book/ }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Approve candidate Current candidate" }),
  );
  expect(await screen.findByText(/action outcome is unknown/)).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Approve candidate Current candidate" }),
  ).toBeDisabled();
  mocks.detail.mockResolvedValue({ ...row(), request: { ...row().request, revision: 8 } });
  fireEvent.click(screen.getByRole("button", { name: "Reload acquisition" }));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Approve candidate Current candidate" }),
    ).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Approve candidate Current candidate" }));
  await waitFor(() =>
    expect(mocks.approveCandidate).toHaveBeenLastCalledWith(
      "request-a",
      8,
      "abook:2",
      expect.objectContaining({ key: "organization:a" }),
    ),
  );
});
it("recovers binding after an unknown creation reload without permitting duplicate creation", async () => {
  const created = {
    id: "connection-recovered",
    name: "Recovered connection",
    installation_id: 4,
    capability_id: "audiobooks",
    library_id: 0,
    download_roots: [],
    binding_revision: 1,
    ready: false,
  };
  mocks.createIntegration.mockRejectedValue(new TypeError("Unknown outcome"));
  mocks.setLibrary.mockResolvedValue({ ...created, library_id: 3 });
  open();
  fireEvent.change(await screen.findByLabelText("Connection name"), {
    target: { value: "Recovered connection" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add audiobook connection" }));
  expect(await screen.findByText(/Connection outcome is unknown/)).toBeInTheDocument();
  mocks.setup.mockResolvedValue({ ...setup, integrations: [created] });
  fireEvent.click(screen.getByRole("button", { name: "Reload connections" }));
  const library = await screen.findByLabelText("Audiobook library");
  expect(library).toBeEnabled();
  expect(screen.getByRole("button", { name: "Add audiobook connection" })).toBeDisabled();
  fireEvent.change(library, { target: { value: "3" } });
  fireEvent.change(screen.getByLabelText(/Download roots \(one per line\)/), {
    target: { value: "/synthetic/audio/recovered" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save audiobook library and roots" }));
  await waitFor(() =>
    expect(mocks.setLibrary).toHaveBeenCalledWith(
      "connection-recovered",
      3,
      ["/synthetic/audio/recovered"],
      expect.objectContaining({ key: "organization:a" }),
    ),
  );
  expect(mocks.createIntegration).toHaveBeenCalledTimes(1);
});
it("clears unsaved binding drafts when creation selects a different connection", async () => {
  const previous = {
    id: "connection-old",
    name: "Existing connection",
    installation_id: 4,
    capability_id: "audiobooks",
    library_id: 3,
    download_roots: ["/synthetic/audio/existing"],
    binding_revision: 1,
    ready: true,
  };
  const created = {
    ...previous,
    id: "connection-new",
    name: "New connection",
    library_id: 0,
    download_roots: [],
    ready: false,
  };
  mocks.setup.mockResolvedValue({ ...setup, integrations: [previous] });
  mocks.createIntegration.mockImplementation(async () => {
    mocks.setup.mockResolvedValue({ ...setup, integrations: [previous, created] });
    return created;
  });
  open();
  fireEvent.change(await screen.findByLabelText("Audiobook library"), { target: { value: "3" } });
  fireEvent.change(screen.getByLabelText(/Download roots \(one per line\)/), {
    target: { value: "/synthetic/audio/unsaved" },
  });
  fireEvent.change(screen.getByLabelText("Connection name"), {
    target: { value: "New connection" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add audiobook connection" }));
  await waitFor(() =>
    expect(screen.getByLabelText("Audiobook connection")).toHaveValue("connection-new"),
  );
  expect(screen.getByLabelText("Audiobook library")).toHaveValue("");
  expect(screen.getByLabelText(/Download roots \(one per line\)/)).toHaveValue("");
  expect(screen.getByRole("button", { name: "Save audiobook library and roots" })).toBeDisabled();
  expect(mocks.setLibrary).not.toHaveBeenCalled();
});
const savedConnection = {
  id: "connection-a",
  name: "Audio delivery",
  installation_id: 4,
  capability_id: "audiobooks",
  library_id: 3,
  library_name: "Audio",
  download_roots: ["/synthetic/audio/incoming"],
  binding_revision: 2,
  ready: true,
  plugin_config: { qbittorrent_url: "https://client.example.test", qbittorrent_username: "reader" },
  stored_credentials: ["qbittorrent_password"],
};
it("edits a saved connection in place and sends only re-entered credentials", async () => {
  mocks.setup.mockResolvedValue({ ...setup, integrations: [savedConnection] });
  mocks.updateIntegration.mockResolvedValue({ ...savedConnection, name: "Renamed delivery" });
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Edit connection" }));
  const form = screen.getByRole("form", { name: "Edit audiobook connection" });
  expect(within(form).getByLabelText(/^qBittorrent URL/)).toHaveValue(
    "https://client.example.test",
  );
  expect(within(form).getByLabelText("qBittorrent password")).toHaveAttribute(
    "placeholder",
    "Saved; leave blank to keep",
  );
  expect(within(form).getByLabelText("NZBGet password")).toHaveAttribute("placeholder", "Not set");
  // The create form is hidden while editing, so no label is ambiguous.
  expect(
    screen.queryByRole("button", { name: "Add audiobook connection" }),
  ).not.toBeInTheDocument();
  fireEvent.change(within(form).getByLabelText("Connection name"), {
    target: { value: "Renamed delivery" },
  });
  fireEvent.change(within(form).getByLabelText("qBittorrent password"), {
    target: { value: "synthetic-rotated" },
  });
  fireEvent.click(within(form).getByRole("button", { name: "Save connection" }));
  await waitFor(() =>
    expect(mocks.updateIntegration).toHaveBeenCalledWith(
      "connection-a",
      {
        name: "Renamed delivery",
        plugin_config: expect.objectContaining({
          qbittorrent_url: "https://client.example.test",
          qbittorrent_username: "reader",
          qbittorrent_category: "bloem-audiobooks",
        }),
        credentials: { qbittorrent_password: "synthetic-rotated" },
      },
      expect.objectContaining({ key: "organization:a" }),
    ),
  );
  expect(await screen.findByText("Audiobook connection saved.")).toBeInTheDocument();
  expect(screen.queryByRole("form", { name: "Edit audiobook connection" })).not.toBeInTheDocument();
});
it("keeps a refused edit open without the typed secret or a reload block", async () => {
  mocks.setup.mockResolvedValue({ ...setup, integrations: [savedConnection] });
  mocks.updateIntegration.mockRejectedValue(
    new AdminV2ClientError(
      400,
      "invalid_request",
      "invalid book request: enter the qBittorrent password again because its URL changed",
    ),
  );
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Edit connection" }));
  const form = screen.getByRole("form", { name: "Edit audiobook connection" });
  fireEvent.change(within(form).getByLabelText(/^qBittorrent URL/), {
    target: { value: "https://moved.example.test" },
  });
  fireEvent.change(within(form).getByLabelText("NZBGet password"), {
    target: { value: "synthetic-nzb" },
  });
  fireEvent.click(within(form).getByRole("button", { name: "Save connection" }));
  expect(await within(form).findByText(/because its URL changed/)).toBeInTheDocument();
  expect(within(form).getByLabelText("NZBGet password")).toHaveValue("");
  expect(within(form).getByLabelText(/^qBittorrent URL/)).toHaveValue("https://moved.example.test");
  expect(within(form).getByRole("button", { name: "Save connection" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Test connection" })).toBeEnabled();
});
it("reports the plugin's connection test verdict for the selected connection", async () => {
  mocks.setup.mockResolvedValue({ ...setup, integrations: [savedConnection] });
  mocks.testIntegration.mockResolvedValue({
    ok: false,
    message: "One or more connection checks failed.",
  });
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Test connection" }));
  await waitFor(() =>
    expect(mocks.testIntegration).toHaveBeenCalledWith(
      "connection-a",
      expect.objectContaining({ key: "organization:a" }),
    ),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Connection test failed: One or more connection checks failed.",
  );
});
it("offers organization admins neither connection tests nor edits", async () => {
  mocks.active = org("a", "organization_admin");
  mocks.setup.mockResolvedValue({ ...setup, integrations: [savedConnection] });
  open();
  expect(await screen.findByText(/Download roots:/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Test connection" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Edit connection" })).not.toBeInTheDocument();
});
it("warns before a download client URL change and blocks edits of unread settings", async () => {
  const unread = { ...savedConnection, id: "connection-b", name: "Unread delivery" };
  delete (unread as Partial<typeof savedConnection>).plugin_config;
  mocks.setup.mockResolvedValue({ ...setup, integrations: [savedConnection, unread] });
  open();
  fireEvent.click(await screen.findByRole("button", { name: "Edit connection" }));
  const form = screen.getByRole("form", { name: "Edit audiobook connection" });
  expect(within(form).queryByText(/fail their status checks/)).not.toBeInTheDocument();
  fireEvent.change(within(form).getByLabelText(/^qBittorrent URL/), {
    target: { value: "https://moved.example.test" },
  });
  expect(within(form).getByRole("alert")).toHaveTextContent(
    "Changing the qBittorrent URL makes downloads already handed to the previous client fail their status checks.",
  );
  fireEvent.click(within(form).getByRole("button", { name: "Cancel" }));
  fireEvent.change(screen.getByLabelText("Audiobook connection"), {
    target: { value: "connection-b" },
  });
  expect(screen.getByRole("button", { name: "Edit connection" })).toBeDisabled();
  expect(screen.getByText(/saved settings could not be read/)).toBeInTheDocument();
  expect(mocks.updateIntegration).not.toHaveBeenCalled();
});
