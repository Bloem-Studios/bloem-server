// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { adminV2Api, AdminV2ClientError } from "@/api/adminV2Client";
import { OrganizationDomainsPanel } from "./OrganizationDomainsPanel";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

const ORG = "22222222-2222-2222-2222-222222222222";
const domains = [
  {
    host: "main.domain.com",
    organization_id: ORG,
    primary: true,
    source: "platform",
    created_at: "2026-11-01T00:00:00Z",
  },
  {
    host: "watch.domain.com",
    organization_id: ORG,
    primary: false,
    source: "garden",
    created_at: "2026-11-01T00:01:00Z",
  },
];

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <OrganizationDomainsPanel organizationID={ORG} />
    </QueryClientProvider>,
  );
}

function answer(write?: (path: string, init?: RequestInit) => unknown) {
  vi.mocked(adminV2Api).mockImplementation(async (path, init) => {
    if (!init?.method || init.method === "GET") return { domains };
    return write ? write(String(path), init) : undefined;
  });
}

afterEach(() => {
  cleanup();
  vi.mocked(adminV2Api).mockReset();
});

it("lists the organization's domains with the primary marked", async () => {
  answer();
  renderPanel();
  expect(await screen.findByText("main.domain.com")).toBeTruthy();
  expect(screen.getByText("watch.domain.com")).toBeTruthy();
  expect(screen.getAllByText("Primary")).toHaveLength(1);
  expect(vi.mocked(adminV2Api).mock.calls[0]?.[0]).toBe(`/platform/tenants/${ORG}/domains`);
});

it("adds a domain with the host and the primary choice", async () => {
  answer(() => ({ domain: domains[0] }));
  renderPanel();
  await screen.findByText("main.domain.com");
  fireEvent.change(screen.getByLabelText("Domain"), { target: { value: "new.domain.com" } });
  fireEvent.click(screen.getByLabelText("Make it the primary domain"));
  fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
  await waitFor(() => {
    const post = vi.mocked(adminV2Api).mock.calls.find(([, init]) => init?.method === "POST");
    expect(post?.[0]).toBe(`/platform/tenants/${ORG}/domains`);
    expect(JSON.parse(String(post?.[1]?.body))).toEqual({ host: "new.domain.com", primary: true });
  });
});

it("makes a domain primary", async () => {
  answer();
  renderPanel();
  await screen.findByText("watch.domain.com");
  fireEvent.click(screen.getByRole("button", { name: "Make watch.domain.com primary" }));
  await waitFor(() =>
    expect(
      vi
        .mocked(adminV2Api)
        .mock.calls.some(
          ([path, init]) =>
            path === `/platform/tenants/${ORG}/domains/watch.domain.com/primary` &&
            init?.method === "PUT",
        ),
    ).toBe(true),
  );
});

it("asks before removing a domain", async () => {
  answer();
  renderPanel();
  await screen.findByText("watch.domain.com");
  fireEvent.click(screen.getByRole("button", { name: "Remove watch.domain.com" }));
  expect(vi.mocked(adminV2Api).mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(
    false,
  );
  fireEvent.click(screen.getByRole("button", { name: "Confirm removing watch.domain.com" }));
  await waitFor(() =>
    expect(
      vi
        .mocked(adminV2Api)
        .mock.calls.some(
          ([path, init]) =>
            path === `/platform/tenants/${ORG}/domains/watch.domain.com` &&
            init?.method === "DELETE",
        ),
    ).toBe(true),
  );
});

it("shows why a domain could not be added", async () => {
  answer(() => {
    throw new AdminV2ClientError(409, "domain_taken", "The host is already registered");
  });
  renderPanel();
  await screen.findByText("main.domain.com");
  fireEvent.change(screen.getByLabelText("Domain"), { target: { value: "taken.domain.com" } });
  fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
  expect((await screen.findByRole("alert")).textContent).toContain(
    "The host is already registered",
  );
});
