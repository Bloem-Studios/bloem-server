import type { ReactNode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AdminContextSummary } from "@/api/bloemTypes";
import type { BookRequest } from "@/api/bloemBookRequests";
import { activateAdminV2Context } from "@/api/adminV2Client";
import { setAccessToken } from "@/api/client";

const mocks = vi.hoisted(() => ({
  supported: true,
  ebookSupported: false,
  ebookList: vi.fn(),
  ebookAction: vi.fn(),
  integrations: vi.fn(),
  setLibrary: vi.fn(),
  createIntegration: vi.fn(),
  setPolicy: vi.fn(),
  list: vi.fn(),
  action: vi.fn(),
  active: null as AdminContextSummary | null,
  setActive: (_active: AdminContextSummary | null) => {},
}));
vi.mock("@/contexts/AdminContextProvider", async () => {
  const React = await import("react");
  const Context = React.createContext<AdminContextSummary | null>(null);
  return {
    useAdminContext: () => ({ active: React.useContext(Context), switching: false }),
    AdminContextProvider: ({ children }: { children: ReactNode }) => {
      const [active, setActive] = React.useState(mocks.active);
      mocks.setActive = setActive;
      return <Context.Provider value={active}>{children}</Context.Provider>;
    },
  };
});
vi.mock("@/pages/AdminRequests", () => ({ default: () => <h1>Video request queue</h1> }));
vi.mock("@/api/bloemClient", () => ({
  nativeApi: async () => ({
    feature_tokens: [
      ...(mocks.supported ? ["audiobook_requests_v1"] : []),
      ...(mocks.ebookSupported ? ["ebook_requests_v1"] : []),
    ],
  }),
}));
vi.mock("@/api/bloemBookRequests", async () => {
  const actual =
    await vi.importActual<typeof import("@/api/bloemBookRequests")>("@/api/bloemBookRequests");
  return {
    ...actual,
    bookRequestsApi: { adminList: mocks.list, adminAction: mocks.action },
    ebookRequestsApi: {
      adminList: mocks.ebookList,
      adminAction: mocks.ebookAction,
      integrations: mocks.integrations,
      setLibrary: mocks.setLibrary,
      createIntegration: mocks.createIntegration,
      setPolicy: mocks.setPolicy,
    },
  };
});
import { AdminContextProvider } from "@/contexts/AdminContextProvider";
import BloemAdminRequests from "./BloemAdminRequests";

function org(id: string): AdminContextSummary {
  return {
    key: `organization:${id}`,
    scope: "organization",
    organizationId: id,
    name: `Organization ${id}`,
    status: "active",
    authority: "organization_admin",
    policyRevision: 1,
    securityRevision: 1,
  };
}
function request(title: string): BookRequest {
  return {
    id: title,
    organization_id: "org-a",
    media_type: "audiobook",
    title,
    author: "Author",
    status: "pending",
    outcome: "active",
    fulfilment: "awaiting_fulfilment",
    revision: 5,
    requested_by_user_id: 1,
    requested_by_profile_id: "viewer",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-10T00:00:00Z",
  };
}
function open(url = "/admin/requests?kind=audiobook") {
  render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
        })
      }
    >
      <AdminContextProvider platformAuthority={true}>
        <MemoryRouter initialEntries={[url]}>
          <BloemAdminRequests />
        </MemoryRouter>
      </AdminContextProvider>
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = vi.fn();
    },
  );
  mocks.supported = true;
  mocks.ebookSupported = false;
  mocks.active = org("a");
  setAccessToken("account-a");
  activateAdminV2Context("admin-a", "organization:a");
  mocks.list.mockReset().mockResolvedValue({ requests: [] });
  mocks.action.mockReset().mockResolvedValue({});
  mocks.ebookList.mockReset().mockResolvedValue({ requests: [] });
  mocks.ebookAction.mockReset().mockResolvedValue({});
  mocks.integrations.mockReset().mockResolvedValue({
    available: true,
    enabled: false,
    integrations: [{ id: "connection-a", name: "Books", library_id: 10 }],
    libraries: [
      { id: 10, name: "Ebooks" },
      { id: 11, name: "Other ebooks" },
    ],
    installations: [{ id: 3, name: "Bookwarehouse", capability_id: "request-router" }],
  });
  mocks.setLibrary.mockReset().mockResolvedValue({});
  mocks.createIntegration
    .mockReset()
    .mockResolvedValue({ id: "new-connection", name: "New books" });
  mocks.setPolicy.mockReset().mockImplementation(async (enabled: boolean) => ({ enabled }));
});
afterEach(() => vi.unstubAllGlobals());

describe("organization ebook queue", () => {
  it("saves ebook acquisition opt-in separately even when no provider is configured", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    mocks.integrations.mockResolvedValue({
      available: true,
      enabled: false,
      integrations: [],
      libraries: [],
      installations: [],
    });
    open("/admin/requests?kind=ebook");
    const toggle = await screen.findByRole("checkbox", { name: "Enable ebook requests" });
    expect(toggle).not.toBeChecked();
    const save = screen.getByRole("button", { name: "Save ebook request policy" });
    expect(save).toBeDisabled();
    expect(screen.getByText(/Bookwarehouse can acquire books/)).toBeInTheDocument();
    fireEvent.click(toggle);
    expect(mocks.setPolicy).not.toHaveBeenCalled();
    fireEvent.click(save);
    await waitFor(() =>
      expect(mocks.setPolicy).toHaveBeenCalledWith(
        true,
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
    await waitFor(() => expect(save).toBeDisabled());
    expect(toggle).toBeChecked();
    expect(mocks.createIntegration).not.toHaveBeenCalled();
    expect(mocks.setLibrary).not.toHaveBeenCalled();
  });
  it("keeps a failed policy save inline and permits a deliberate retry", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    mocks.setPolicy.mockRejectedValueOnce(new Error("Policy service unavailable"));
    open("/admin/requests?kind=ebook");
    fireEvent.click(await screen.findByRole("checkbox", { name: "Enable ebook requests" }));
    const save = screen.getByRole("button", { name: "Save ebook request policy" });
    fireEvent.click(save);
    expect(await screen.findByRole("alert")).toHaveTextContent("Policy service unavailable");
    expect(mocks.setPolicy).toHaveBeenCalledOnce();
    fireEvent.click(save);
    await waitFor(() => expect(mocks.setPolicy).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
  });
  it("clears cached ebook rows and setup credentials after account identity changes", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    mocks.ebookList.mockResolvedValueOnce({ requests: [request("Old account ebook")] });
    let finish!: (value: { requests: BookRequest[] }) => void;
    mocks.ebookList.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    open("/admin/requests?kind=ebook");
    await screen.findByText("Old account ebook");
    fireEvent.change(await screen.findByLabelText("API key"), {
      target: { value: "private-draft-key" },
    });
    fireEvent.click(screen.getByRole("checkbox", { name: "Enable ebook requests" }));
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Allow HTTP for a trusted local server" }),
    );
    await act(async () => {
      setAccessToken("account-b");
      mocks.setActive({ ...org("a"), authority: "platform_admin" });
    });
    expect(screen.queryByText("Old account ebook")).not.toBeInTheDocument();
    expect(await screen.findByLabelText("API key")).toHaveValue("");
    expect(screen.getByRole("checkbox", { name: "Enable ebook requests" })).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Allow HTTP for a trusted local server" }),
    ).not.toBeChecked();
    await act(async () => {
      finish({ requests: [request("New account ebook")] });
    });
    await screen.findByText("New account ebook");
  });
  it("creates an explicitly ebook connection and clears the supplied key after success", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    open("/admin/requests?kind=ebook");
    const name = await screen.findByRole("textbox", { name: "Connection name" });
    fireEvent.change(name, { target: { value: "New books" } });
    fireEvent.change(screen.getByRole("textbox", { name: "Base URL" }), {
      target: { value: "https://books.example" },
    });
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "synthetic-provider-key" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add connection" }));
    await waitFor(() =>
      expect(mocks.createIntegration).toHaveBeenCalledWith(
        {
          name: "New books",
          base_url: "https://books.example",
          api_key: "synthetic-provider-key",
          installation_id: 3,
          capability_id: "request-router",
          allow_http: false,
        },
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
    await waitFor(() => expect(screen.getByLabelText("API key")).toHaveValue(""));
  });
  it("requires explicit trusted-local opt-in for HTTP and resets it after connection creation", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    open("/admin/requests?kind=ebook");
    fireEvent.change(await screen.findByLabelText("Connection name"), {
      target: { value: "Local books" },
    });
    fireEvent.change(screen.getByLabelText("Base URL"), {
      target: { value: "http://books.example" },
    });
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "synthetic-provider-key" },
    });
    const optIn = screen.getByRole("checkbox", { name: "Allow HTTP for a trusted local server" });
    expect(optIn).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Add connection" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Add connection" }));
    expect(mocks.createIntegration).not.toHaveBeenCalled();
    fireEvent.click(optIn);
    expect(screen.getByRole("button", { name: "Add connection" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "Add connection" }));
    await waitFor(() =>
      expect(mocks.createIntegration).toHaveBeenCalledWith(
        {
          name: "Local books",
          base_url: "http://books.example",
          api_key: "synthetic-provider-key",
          installation_id: 3,
          capability_id: "request-router",
          allow_http: true,
        },
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
    await waitFor(() => expect(optIn).not.toBeChecked());
    expect(screen.getByLabelText("API key")).toHaveValue("");
  });
  it("does not accept other URL schemes even when local HTTP is allowed", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    open("/admin/requests?kind=ebook");
    fireEvent.change(await screen.findByLabelText("Base URL"), {
      target: { value: "ftp://books.example" },
    });
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Allow HTTP for a trusted local server" }),
    );
    expect(screen.getByRole("button", { name: "Add connection" })).toBeDisabled();
    expect(mocks.createIntegration).not.toHaveBeenCalled();
  });
  it("links the Plugins page when no eligible ebook request plugin is installed", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    mocks.integrations.mockResolvedValue({
      available: true,
      integrations: [],
      libraries: [],
      installations: [],
    });
    open("/admin/requests?kind=ebook");
    expect(
      await screen.findByRole("link", { name: "Install the Bookwarehouse request plugin" }),
    ).toHaveAttribute("href", "/admin/plugins");
    expect(screen.queryByLabelText("API key")).not.toBeInTheDocument();
  });
  it("reviews and retries ebooks without exposing manual fulfilment", async () => {
    mocks.ebookSupported = true;
    const pending = { ...request("Pending ebook"), media_type: "book" as const };
    const failed = {
      ...request("Failed ebook"),
      media_type: "book" as const,
      status: "approved" as const,
      fulfilment: "failed",
    };
    mocks.ebookList.mockResolvedValue({ requests: [pending, failed] });
    open("/admin/requests?kind=ebook");
    await screen.findByText("Pending ebook");
    expect(screen.queryByRole("button", { name: "Mark fulfilled" })).not.toBeInTheDocument();
    expect(screen.getByText("Approved · fulfilment failed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(mocks.ebookAction).toHaveBeenCalledWith(
        pending,
        "approve",
        "",
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Retry fulfilment" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry fulfilment" }));
    await waitFor(() =>
      expect(mocks.ebookAction).toHaveBeenCalledWith(
        failed,
        "retry",
        "",
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
    expect(mocks.action).not.toHaveBeenCalled();
    expect(mocks.integrations).not.toHaveBeenCalled();
  });
  it("shows unavailable ebooks without querying a missing contract", async () => {
    open("/admin/requests?kind=ebook");
    await screen.findByText("Ebook requests are unavailable on this server.");
    expect(mocks.ebookList).not.toHaveBeenCalled();
  });
  it("limits linked-library setup to platform authority in the current organization", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    open("/admin/requests?kind=ebook");
    await screen.findByRole("heading", { name: "Ebook fulfillment" });
    const library = await screen.findByRole("combobox", { name: "Library" });
    expect(library).toHaveValue("10");
    fireEvent.change(library, { target: { value: "11" } });
    fireEvent.click(screen.getByRole("button", { name: "Save library" }));
    await waitFor(() =>
      expect(mocks.setLibrary).toHaveBeenCalledWith(
        "connection-a",
        11,
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
    await act(async () => {
      activateAdminV2Context("admin-b", "organization:b");
      mocks.setActive({ ...org("b"), authority: "platform_admin" });
    });
    await waitFor(() =>
      expect(mocks.integrations.mock.calls.at(-1)?.[0]?.key).toBe("organization:b"),
    );
    expect(screen.getByRole("combobox", { name: "Library" })).toHaveValue("10");
  });
  it("shows a hosted restriction without editing a linked library", async () => {
    mocks.ebookSupported = true;
    mocks.active = { ...org("a"), authority: "platform_admin" };
    mocks.integrations.mockResolvedValue({ available: false, integrations: [], libraries: [] });
    open("/admin/requests?kind=ebook");
    await screen.findByText("Ebook fulfillment is available only in the default organization.");
    expect(screen.queryByRole("combobox", { name: "Library" })).not.toBeInTheDocument();
  });
});

describe("organization audiobook queue", () => {
  it("preserves audiobook manual fulfilment", async () => {
    const row = { ...request("Manual audiobook"), status: "approved" as const };
    mocks.list.mockResolvedValue({ requests: [row] });
    open();
    await screen.findByText("Manual audiobook");
    fireEvent.click(screen.getByRole("button", { name: "Mark fulfilled" }));
    await waitFor(() =>
      expect(mocks.action).toHaveBeenCalledWith(
        row,
        "fulfil",
        "",
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
    expect(mocks.ebookAction).not.toHaveBeenCalled();
  });
  it("requires an organization instead of granting a platform-wide queue", async () => {
    mocks.active = {
      key: "platform",
      scope: "platform",
      name: "Platform",
      status: "active",
      authority: "platform_admin",
      policyRevision: 1,
      securityRevision: 1,
    };
    activateAdminV2Context("platform", "platform");
    open();
    await screen.findByText(/Select an organization in the navigation/);
    expect(mocks.list).not.toHaveBeenCalled();
  });
  it("falls back to the native video queue without audiobook capability", async () => {
    mocks.supported = false;
    open();
    await screen.findByRole("heading", { name: "Video request queue" });
    expect(mocks.list).not.toHaveBeenCalled();
  });
  it("submits a reviewed row and reason under the selected organization", async () => {
    const row = request("Review book");
    mocks.list.mockResolvedValue({ requests: [row] });
    open();
    await screen.findByText("Review book");
    fireEvent.change(screen.getByRole("textbox", { name: "Reason (optional)" }), {
      target: { value: "Edition checked" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() =>
      expect(mocks.action).toHaveBeenCalledWith(
        row,
        "approve",
        "Edition checked",
        expect.objectContaining({ key: "organization:a" }),
      ),
    );
  });
  it("resets pagination, rows and reasons when administrative context changes", async () => {
    mocks.list.mockImplementation(async (context, offset) => ({
      requests:
        context.key === "organization:a"
          ? offset === 0
            ? Array.from({ length: 100 }, (_, i) => request(`A book ${i}`))
            : [request("A second page")]
          : [request("B book")],
    }));
    open();
    await screen.findByText("A book 0");
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByText("A second page");
    fireEvent.change(screen.getByRole("textbox", { name: "Reason (optional)" }), {
      target: { value: "A private reason" },
    });
    await act(async () => {
      activateAdminV2Context("admin-b", "organization:b");
      mocks.setActive(org("b"));
    });
    await screen.findByText("B book");
    expect(screen.queryByText("A second page")).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Reason (optional)" })).toHaveValue("");
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
    expect(mocks.list.mock.calls.at(-1)?.[0]?.key).toBe("organization:b");
    expect(mocks.list.mock.calls.at(-1)?.[1]).toBe(0);
  });
});
