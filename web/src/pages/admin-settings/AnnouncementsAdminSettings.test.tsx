import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/api/client";
import AnnouncementsAdminSettings from "./AnnouncementsAdminSettings";

vi.mock("@/api/client", () => ({ api: vi.fn() }));
vi.mock("@/hooks/useUnsavedChanges", () => ({ useReportUnsavedChanges: vi.fn() }));
const mockApi = vi.mocked(api);
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
  mockApi.mockReset();
  mockApi.mockResolvedValue({ announcements: [] });
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
        "/admin/notifications/announcements",
        expect.objectContaining({ method: "POST" }),
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
      expect(mockApi).toHaveBeenCalledWith("/admin/notifications/announcements/a1", {
        method: "DELETE",
      }),
    );
  });
  it("shows the server error while preserving the draft", async () => {
    mockApi.mockRejectedValue(new Error("Announcements are unavailable"));
    renderPage();
    expect(await screen.findByText("Announcements are unavailable")).toBeInTheDocument();
    expect(screen.getByLabelText("Title")).toBeEnabled();
  });
});
