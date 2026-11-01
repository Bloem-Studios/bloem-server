// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { adminV2Api, AdminV2ClientError } from "@/api/adminV2Client";
import { OrganizationBrandingPanel } from "./OrganizationBrandingPanel";

vi.mock("@/api/adminV2Client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/adminV2Client")>();
  return { ...actual, adminV2Api: vi.fn() };
});

const ORG = "22222222-2222-2222-2222-222222222222";
const branding = {
  server_name: "Main",
  login_subtitle: "",
  accent_color: "#336699",
  default_theme: "",
  assets: { mark: "abc.webp" },
};

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <OrganizationBrandingPanel organizationID={ORG} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.mocked(adminV2Api).mockReset();
});

it("saves only the fields that changed", async () => {
  vi.mocked(adminV2Api).mockImplementation(async (_path, init) =>
    init?.method === "PUT" ? branding : branding,
  );
  renderPanel();
  const name = (await screen.findByLabelText("Server name")) as HTMLInputElement;
  expect(name.value).toBe("Main");
  fireEvent.change(screen.getByLabelText("Login subtitle"), {
    target: { value: "Welcome to Main" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save branding" }));
  await waitFor(() => {
    const put = vi.mocked(adminV2Api).mock.calls.find(([, init]) => init?.method === "PUT");
    expect(put?.[0]).toBe(`/platform/tenants/${ORG}/branding`);
    expect(JSON.parse(String(put?.[1]?.body))).toEqual({ login_subtitle: "Welcome to Main" });
  });
});

it("uploads an image as multipart to the asset's slot", async () => {
  vi.mocked(adminV2Api).mockImplementation(async (_path, init) =>
    init?.method === "PUT" ? { kind: "mark", ref: "def.webp" } : branding,
  );
  renderPanel();
  const input = await screen.findByLabelText("Upload Mark");
  fireEvent.change(input, {
    target: { files: [new File(["png"], "mark.png", { type: "image/png" })] },
  });
  await waitFor(() => {
    const put = vi
      .mocked(adminV2Api)
      .mock.calls.find(
        ([path, init]) => init?.method === "PUT" && String(path).endsWith("/branding/assets/mark"),
      );
    expect(put?.[0]).toBe(`/platform/tenants/${ORG}/branding/assets/mark`);
    expect(put?.[1]?.body).toBeInstanceOf(FormData);
    expect((put?.[1]?.body as FormData).get("file")).toBeInstanceOf(Blob);
  });
});

it("points the default organization at the server's branding settings", async () => {
  vi.mocked(adminV2Api).mockRejectedValue(
    new AdminV2ClientError(
      409,
      "default_organization",
      "The default organization uses the server's branding settings",
    ),
  );
  renderPanel();
  expect(
    await screen.findByText("The default organization uses the server's branding settings."),
  ).toBeTruthy();
  expect(screen.queryByLabelText("Server name")).toBeNull();
});
