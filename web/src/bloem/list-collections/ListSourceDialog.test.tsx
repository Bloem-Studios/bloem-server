import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ListSourceDialog } from "./ListSourceDialog";
import { ListSyncStatus } from "./ListSyncStatus";

const api = vi.hoisted(() => ({
  preview: vi.fn(),
  getPreview: vi.fn(),
  create: vi.fn(),
  get: vi.fn(),
  refresh: vi.fn(),
  disconnect: vi.fn(),
}));
vi.mock("./api", () => ({ listActorKey: () => ["fixture"], listCollectionsApi: api }));
vi.mock("@/hooks/queries/libraries", () => ({
  useUserLibraries: () => ({ data: [{ id: 7, name: "Movies" }] }),
}));
const capability = {
  available: true,
  connections: [{ installation_id: 1, origin: "https://pastime.example", can_create_server: true }],
};
function mount(element: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{element}</QueryClientProvider>);
}
beforeEach(() => {
  vi.resetAllMocks();
  api.preview.mockResolvedValue({ job_id: "preview-1" });
  api.getPreview.mockResolvedValue({
    id: "preview-1",
    status: "resolved",
    name: "Weekend movies",
    kind: "manual",
    count: 2,
    expires_at: new Date(Date.now() + 60000).toISOString(),
  });
  api.create.mockResolvedValue({ id: "binding-1" });
});

describe("Pastime list workflow", () => {
  it("previews and creates an explicitly shared personal collection", async () => {
    const user = userEvent.setup();
    const created = vi.fn();
    mount(<ListSourceDialog capability={capability} onClose={vi.fn()} onCreated={created} />);
    await user.type(screen.getByLabelText("List URL"), "https://pastime.example/lists/1");
    await user.click(screen.getByRole("button", { name: "Preview list" }));
    await screen.findByText("Weekend movies · 2 titles");
    await user.click(screen.getByLabelText("Share with other profiles on my account"));
    await user.click(screen.getByRole("button", { name: "Link collection" }));
    expect(api.create).toHaveBeenCalledWith({
      preview_id: "preview-1",
      scope: "personal",
      name: "Weekend movies",
      is_shared: true,
    });
    expect(created).toHaveBeenCalledWith({ id: "binding-1" });
  });
  it("requires a selected library for server creation", async () => {
    const user = userEvent.setup();
    mount(
      <ListSourceDialog
        capability={capability}
        scope="server"
        onClose={vi.fn()}
        onCreated={vi.fn()}
      />,
    );
    await user.type(screen.getByLabelText("List URL"), "https://pastime.example/lists/1");
    await user.click(screen.getByRole("button", { name: "Preview list" }));
    await screen.findByText("Weekend movies · 2 titles");
    expect(screen.getByRole("button", { name: "Link collection" })).toBeDisabled();
    await user.click(screen.getByLabelText("Movies"));
    await user.click(screen.getByRole("button", { name: "Link collection" }));
    expect(api.create).toHaveBeenCalledWith({
      preview_id: "preview-1",
      scope: "server",
      name: "Weekend movies",
      is_shared: false,
      library_ids: [7],
    });
  });
  it("preserves failed preview and never creates", async () => {
    api.getPreview.mockResolvedValue({ status: "failed", failure: "denied" });
    const user = userEvent.setup();
    mount(<ListSourceDialog capability={capability} onClose={vi.fn()} onCreated={vi.fn()} />);
    await user.type(screen.getByLabelText("List URL"), "https://pastime.example/lists/1");
    await user.click(screen.getByRole("button", { name: "Preview list" }));
    await screen.findByText("Preview unavailable. Check access and try again.");
    expect(screen.queryByRole("button", { name: "Link collection" })).not.toBeInTheDocument();
    expect(api.create).not.toHaveBeenCalled();
  });
  it("shows separate refresh failure and quota outcomes with retained membership", async () => {
    api.get.mockResolvedValue({
      id: "binding-1",
      status: "failed",
      failure: "unavailable",
      last_success: "2026-10-13T00:00:00Z",
      matched: 4,
      unmatched: [],
      requests: { pending: 1, blocked: 2, delivered: 3, quota: 2, cancelled: 0 },
    });
    mount(<ListSyncStatus id="binding-1" />);
    await screen.findByText("Requests waiting for quota: 2");
    expect(screen.getByText(/The last successful collection is retained/)).toBeVisible();
    expect(screen.getByText(/4 available titles/)).toBeVisible();
  });
});
