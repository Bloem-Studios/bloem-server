import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { activateAdminV2Context, adminV2Api, clearAdminV2Context } from "@/api/adminV2Client";
import AnnouncementsAdminSettings from "./AnnouncementsAdminSettings";

const adminState = vi.hoisted(() => ({
  active: { key: "platform", scope: "platform" } as { key: string; scope: string } | null,
}));
vi.mock("@/api/adminV2Client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/adminV2Client")>()),
  adminV2Api: vi.fn(),
}));
vi.mock("@/contexts/AdminContextProvider", () => ({
  useAdminContext: () => ({ active: adminState.active }),
}));
vi.mock("@/hooks/useUnsavedChanges", () => ({ useReportUnsavedChanges: vi.fn() }));
const mockApi = vi.mocked(adminV2Api);
function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AnnouncementsAdminSettings />
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  adminState.active = { key: "platform", scope: "platform" };
  activateAdminV2Context("test-authority", "platform");
  mockApi.mockReset();
  mockApi.mockResolvedValue({ announcements: [] });
});
afterEach(() => {
  clearAdminV2Context();
});
describe("server admin announcements", () => {
  it("requires preview before publishing and makes critical messages non-dismissible", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("Title"), "Maintenance tonight");
    await user.type(screen.getByLabelText("Message"), "Playback may be unavailable.");
    await user.selectOptions(screen.getByLabelText("Severity"), "critical");
    expect(screen.getByLabelText("Allow viewers to dismiss")).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Preview announcement" }));
    expect(mockApi.mock.calls.every(([, options]) => options?.method !== "POST")).toBe(true);
    await user.click(screen.getByRole("button", { name: "Publish announcement" }));
    await waitFor(() =>
      expect(mockApi).toHaveBeenCalledWith(
        "/platform/announcements",
        expect.objectContaining({ method: "POST" }),
        "none",
        expect.objectContaining({ key: "platform" }),
      ),
    );
    const call = mockApi.mock.calls.find(([, options]) => options?.method === "POST");
    expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
      type: "system.announcement",
      body: { title: "Maintenance tonight", severity: "critical", dismissible: false },
      targeting: { audience: "all" },
    });
  });
  it("shows the exact library audience before sending its numeric ID", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("Title"), "New collection");
    await user.type(screen.getByLabelText("Message"), "Available now.");
    await user.selectOptions(screen.getByLabelText("Audience"), "library");
    await user.type(screen.getByLabelText("Library ID"), "42");
    await user.click(screen.getByRole("button", { name: "Preview announcement" }));
    expect(screen.getByText(/Viewers with access to library 42/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Publish announcement" }));
    await waitFor(() =>
      expect(mockApi.mock.calls.some(([, options]) => options?.method === "POST")).toBe(true),
    );
    const call = mockApi.mock.calls.find(([, options]) => options?.method === "POST");
    expect(JSON.parse(String(call?.[1]?.body)).targeting).toEqual({
      audience: "library",
      library_id: 42,
    });
  });
  it("blocks invalid library targeting without publishing", async () => {
    const user = userEvent.setup();
    renderPage();
    await user.type(screen.getByLabelText("Title"), "New collection");
    await user.type(screen.getByLabelText("Message"), "Available now.");
    await user.selectOptions(screen.getByLabelText("Audience"), "library");
    await user.type(screen.getByLabelText("Library ID"), "-1");
    await user.click(screen.getByRole("button", { name: "Preview announcement" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Enter a valid library ID.");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(mockApi.mock.calls.every(([, options]) => options?.method !== "POST")).toBe(true);
  });
  it("withdraws a published item only after the explicit confirmation", async () => {
    mockApi.mockResolvedValue({
      announcements: [
        {
          id: "a1",
          type: "system.announcement",
          body: { title: "Planned work", body: "Tonight", severity: "info", dismissible: true },
          targeting: { audience: "all" },
          recipient_count: 8,
          created_at: "2026-09-05T09:00:00Z",
          withdrawn_at: null,
        },
      ],
    });
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Withdraw Planned work" }));
    expect(mockApi.mock.calls.every(([, options]) => options?.method !== "DELETE")).toBe(true);
    await user.click(screen.getByRole("button", { name: "Withdraw announcement" }));
    await waitFor(() =>
      expect(mockApi).toHaveBeenCalledWith(
        "/platform/announcements/a1",
        { method: "DELETE" },
        "none",
        expect.objectContaining({ key: "platform" }),
      ),
    );
  });
  it("asks for the platform context instead of calling the server", () => {
    adminState.active = { key: "organization:org-1", scope: "organization" };
    renderPage();
    expect(screen.getByRole("alert")).toHaveTextContent("Select the platform context");
    expect(mockApi).not.toHaveBeenCalled();
  });
  it("shows the server error while preserving the draft", async () => {
    mockApi.mockRejectedValue(new Error("Announcements are unavailable"));
    renderPage();
    expect(await screen.findByText("Announcements are unavailable")).toBeInTheDocument();
    expect(screen.getByLabelText("Title")).toBeEnabled();
  });
});
