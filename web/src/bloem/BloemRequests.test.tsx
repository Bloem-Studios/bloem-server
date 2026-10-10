import type { ReactNode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BookRequest, BookRequestCapability } from "@/api/bloemBookRequests";
import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";

const mocks = vi.hoisted(() => ({
  supported: true,
  capability: vi.fn(),
  mine: vi.fn(),
  create: vi.fn(),
  cancel: vi.fn(),
  auth: { user: { id: 1 }, profile: { id: "viewer" } },
  setAuth: (_auth: { user: { id: number }; profile: { id: string } }) => {},
}));
vi.mock("@/hooks/useAuth", async () => {
  const React = await import("react");
  const Context = React.createContext(mocks.auth);
  return {
    useAuth: () => React.useContext(Context),
    AuthProvider: ({ children }: { children: ReactNode }) => {
      const [auth, setAuth] = React.useState(mocks.auth);
      mocks.setAuth = setAuth;
      return <Context.Provider value={auth}>{children}</Context.Provider>;
    },
  };
});
vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: () => {} }));
vi.mock("@/pages/Requests", () => ({ default: () => <h1>Video requests</h1> }));
vi.mock("@/api/bloemClient", () => ({
  nativeApi: async () => ({ feature_tokens: mocks.supported ? ["audiobook_requests_v1"] : [] }),
}));
vi.mock("@/api/bloemBookRequests", async () => {
  const actual =
    await vi.importActual<typeof import("@/api/bloemBookRequests")>("@/api/bloemBookRequests");
  return {
    ...actual,
    bookRequestsApi: {
      capability: mocks.capability,
      mine: mocks.mine,
      create: mocks.create,
      cancel: mocks.cancel,
    },
  };
});
import { AuthProvider } from "@/hooks/useAuth";
import BloemRequests from "./BloemRequests";

const policy: BookRequestCapability = {
  available: true,
  enabled: true,
  can_request: true,
  auto_approve: false,
  unlimited: false,
  limit: 10,
  used: 0,
  remaining: 10,
  window_days: 7,
  fulfilment_backend: "unconfigured",
};
function request(title: string, overrides: Partial<BookRequest> = {}): BookRequest {
  return {
    id: title,
    organization_id: "org-a",
    media_type: "audiobook",
    title,
    author: "Author",
    status: "approved",
    outcome: "active",
    fulfilment: "awaiting_fulfilment",
    revision: 4,
    requested_by_user_id: 1,
    requested_by_profile_id: "viewer",
    created_at: "2026-10-10T00:00:00Z",
    updated_at: "2026-10-10T00:00:00Z",
    ...overrides,
  };
}
function tree(client: QueryClient, url = "/requests?kind=audiobook") {
  return (
    <QueryClientProvider client={client}>
      <AuthProvider>
        <MemoryRouter initialEntries={[url]}>
          <BloemRequests />
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>
  );
}
function open(url?: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(tree(client, url));
  return client;
}
beforeEach(() => {
  mocks.supported = true;
  mocks.auth = { user: { id: 1 }, profile: { id: "viewer" } };
  setAccessToken("account-a");
  setProfileId("viewer");
  setProfileToken(null);
  mocks.capability.mockReset().mockResolvedValue(policy);
  mocks.mine.mockReset().mockResolvedValue({ requests: [] });
  mocks.create.mockReset().mockResolvedValue(request("New book"));
  mocks.cancel.mockReset().mockResolvedValue(request("Cancelled", { outcome: "cancelled" }));
});

describe("audiobook Requests hub", () => {
  it("opens the native video page until audiobook capability is advertised", async () => {
    mocks.supported = false;
    open();
    await screen.findByRole("heading", { name: "Video requests" });
    expect(
      screen.queryByRole("navigation", { name: "Request media type" }),
    ).not.toBeInTheDocument();
    expect(mocks.capability).not.toHaveBeenCalled();
  });
  it("uses the same route to move between video and audiobook requests", async () => {
    open("/requests");
    await screen.findByRole("button", { name: "Audiobooks" });
    expect(screen.getByRole("heading", { name: "Video requests" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Audiobooks" }));
    await screen.findByRole("heading", { name: "Audiobook requests" });
    fireEvent.click(screen.getByRole("button", { name: "Movies & series" }));
    expect(screen.getByRole("heading", { name: "Video requests" })).toBeInTheDocument();
  });
  it("retains the filing key after an ambiguous failure and changes it when the form changes", async () => {
    mocks.create
      .mockRejectedValueOnce(new Error("Connection lost"))
      .mockRejectedValueOnce(new Error("Connection lost"));
    open();
    const title = await screen.findByRole("textbox", { name: "Title" });
    const author = screen.getByRole("textbox", { name: "Author" });
    fireEvent.change(title, { target: { value: "New book" } });
    fireEvent.change(author, { target: { value: "Author" } });
    const submit = screen.getByRole("button", { name: "Request audiobook" });
    fireEvent.click(submit);
    await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(submit).toBeEnabled());
    expect(mocks.create.mock.calls[1]?.[1]).toBe(mocks.create.mock.calls[0]?.[1]);
    fireEvent.change(title, { target: { value: "Different book" } });
    fireEvent.click(submit);
    await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(3));
    expect(mocks.create.mock.calls[2]?.[1]).not.toBe(mocks.create.mock.calls[0]?.[1]);
  });
  it("resets form, pagination and rows when the profile authority changes", async () => {
    mocks.mine.mockImplementation(async (context, offset) => ({
      requests:
        context.profileId === "viewer"
          ? offset === 0
            ? Array.from({ length: 100 }, (_, i) => request(`Viewer book ${i}`))
            : [request("Viewer second page")]
          : [request("Sibling book", { requested_by_profile_id: "sibling" })],
    }));
    open();
    const title = await screen.findByRole("textbox", { name: "Title" });
    fireEvent.change(title, { target: { value: "Old profile draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByText("Viewer second page");
    await act(async () => {
      setProfileId("sibling");
      mocks.setAuth({ user: { id: 1 }, profile: { id: "sibling" } });
    });
    await screen.findByText("Sibling book");
    expect(screen.getByRole("textbox", { name: "Title" })).toHaveValue("");
    expect(screen.getByRole("button", { name: "Previous" })).toBeDisabled();
    expect(screen.queryByText("Viewer second page")).not.toBeInTheDocument();
    expect(mocks.mine.mock.calls.at(-1)?.[0]?.profileId).toBe("sibling");
    expect(mocks.mine.mock.calls.at(-1)?.[1]).toBe(0);
  });
  it("uses exact row revisions for cancellation and links only catalog completions", async () => {
    const approved = request("Waiting book");
    mocks.mine.mockResolvedValue({
      requests: [
        approved,
        request("Manual book", { status: "completed", fulfilment: "manual" }),
        request("Library book", {
          status: "completed",
          fulfilment: "library",
          library_content_id: "audiobook 1",
        }),
      ],
    });
    open();
    await screen.findByText("Waiting book");
    expect(screen.getByText("Approved · awaiting fulfilment")).toBeInTheDocument();
    expect(screen.getByText("Fulfilled by an admin")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Open audiobook" })).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Open audiobook" })).toHaveAttribute(
      "href",
      "/item/audiobook%201",
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel request" }));
    await waitFor(() =>
      expect(mocks.cancel).toHaveBeenCalledWith(
        approved,
        expect.objectContaining({ profileId: "viewer" }),
      ),
    );
  });
});
