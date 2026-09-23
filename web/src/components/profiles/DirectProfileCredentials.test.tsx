// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import { DirectProfileCredentials } from "./DirectProfileCredentials";
const identity = vi.hoisted(() => ({ generation: 1, primary: true }));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    user: { id: 1, role: "user" },
    profile: { id: "parent", is_primary: identity.primary },
  }),
}));
vi.mock("@/hooks/queries/profiles", () => ({
  useProfiles: () => ({
    data: [{ id: "reader", name: "Reader" }],
    isLoading: false,
    isError: false,
  }),
}));
vi.mock("@/hooks/queries/useBloemCapabilities", () => ({
  useBloemCapabilities: () => ({ data: { feature_tokens: ["profile_credential_management_v1"] } }),
}));
vi.mock("@/api/client", async (original) => ({
  ...(await original<typeof import("@/api/client")>()),
  captureProfileRequestContext: () => ({
    serverOrigin: "https://server.test",
    profileId: "parent",
    authContextVersion: identity.generation,
    profileTokenGeneration: 1,
  }),
  isCapturedProfileAuthorityActive: (scope: { authContextVersion: number }) =>
    scope.authContextVersion === identity.generation,
}));
vi.mock("@/api/bloemClient", async (original) => ({
  ...(await original<typeof import("@/api/bloemClient")>()),
  nativeApiWithProfileRequestContext: vi.fn(),
}));
const request = vi.mocked(nativeApiWithProfileRequestContext);
function setup() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = () => (
    <QueryClientProvider client={client}>
      <DirectProfileCredentials />
    </QueryClientProvider>
  );
  const view = render(tree());
  return { ...view, client, refresh: () => view.rerender(tree()) };
}
beforeEach(() => {
  identity.generation = 1;
  identity.primary = true;
  request.mockReset();
  request.mockResolvedValue({
    profile_id: "reader",
    configured: true,
    login_email: "reader@example.test",
    credential_revision: 2,
  });
});
afterEach(cleanup);
async function selectReader() {
  fireEvent.change(screen.getByLabelText("Profile"), { target: { value: "reader" } });
  await screen.findByRole("form", { name: "Manage sign-in for Reader" });
  fireEvent.change(screen.getByLabelText("Your current account password"), {
    target: { value: "account-secret" },
  });
  fireEvent.click(screen.getByLabelText(/I understand that changing/));
}
it("saves credentials without retries or secrets in the query/mutation caches", async () => {
  const { client } = setup();
  await selectReader();
  fireEvent.change(screen.getByLabelText("New profile password"), {
    target: { value: "profile-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save profile credentials" }));
  await screen.findByText(/Profile credentials saved/);
  const writes = request.mock.calls.filter((call) => call[2]?.method);
  expect(writes).toHaveLength(1);
  expect(writes[0]?.[0]).toBe("/profile-credentials/reader");
  expect(writes[0]?.[3]).toBe("none");
  expect(JSON.parse(String(writes[0]?.[2]?.body))).toEqual({
    expected_revision: 2,
    current_password: "account-secret",
    login_email: "reader@example.test",
    password: "profile-secret",
  });
  expect(screen.getByLabelText("Your current account password")).toHaveValue("");
  expect(screen.getByLabelText("New profile password")).toHaveValue("");
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.state.data),
    ),
  ).not.toMatch(/account-secret|profile-secret/);
  expect(screen.getByText(/browser still uses account sign-in/)).toBeInTheDocument();
});
it("disables credentials with explicit confirmation but no replacement password", async () => {
  setup();
  await selectReader();
  fireEvent.click(screen.getByRole("button", { name: "Disable direct sign-in" }));
  await screen.findByText(/Direct profile sign-in disabled/);
  const write = request.mock.calls.find((call) => call[2]?.method)!;
  expect(write[2]?.method).toBe("DELETE");
  expect(JSON.parse(String(write[2]?.body))).toEqual({
    current_password: "account-secret",
    expected_revision: 2,
  });
});
it("clears secrets and reads back after an ambiguous failure", async () => {
  request.mockImplementation(async (_path, _scope, options) => {
    if (options?.method) throw new Error("Connection lost");
    return {
      profile_id: "reader",
      configured: true,
      login_email: "reader@example.test",
      credential_revision: 2,
    };
  });
  setup();
  await selectReader();
  fireEvent.click(screen.getByRole("button", { name: "Disable direct sign-in" }));
  await screen.findByText("Connection lost");
  await waitFor(() => expect(request.mock.calls.filter((call) => !call[2]?.method).length).toBe(2));
  expect(screen.getByLabelText("Your current account password")).toHaveValue("");
});
it("discards the selected target and password draft on login generation changes", async () => {
  const view = setup();
  await selectReader();
  identity.generation++;
  view.refresh();
  expect(screen.getByLabelText("Profile")).toHaveValue("");
  expect(screen.queryByLabelText("Your current account password")).toBeNull();
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(0);
});
it("does not expose household credential controls on a non-primary viewer profile", () => {
  identity.primary = false;
  setup();
  expect(screen.queryByText("Profile sign-in credentials")).toBeNull();
  expect(request).not.toHaveBeenCalled();
});

it("requires readback and a new confirmation after a revision conflict without replaying the write", async () => {
  const initial = {
    profile_id: "reader",
    configured: true,
    login_email: "reader@example.test",
    credential_revision: 2,
  };
  let finishReadback!: (value: typeof initial) => void;
  const readback = new Promise<typeof initial>((resolve) => {
    finishReadback = resolve;
  });
  let reads = 0;
  request.mockImplementation(async (_path, _scope, options) => {
    if (options?.method)
      throw new Error("Credentials changed. Reload and review before trying again.");
    return ++reads === 1 ? initial : readback;
  });
  const { client } = setup();
  await selectReader();
  fireEvent.change(screen.getByLabelText("New profile password"), {
    target: { value: "profile-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Disable direct sign-in" }));
  await screen.findByText(/Credentials changed/);
  await waitFor(() => expect(reads).toBe(2));
  expect(screen.getByLabelText("Your current account password")).toHaveValue("");
  expect(screen.getByLabelText("New profile password")).toHaveValue("");
  expect(screen.getByLabelText(/I understand that changing/)).not.toBeChecked();
  expect(screen.getByRole("button", { name: "Disable direct sign-in" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
  fireEvent.submit(screen.getByRole("form"));
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(1);

  await act(async () =>
    finishReadback({ ...initial, credential_revision: 3, login_email: "new@example.test" }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Disable direct sign-in" })).toBeEnabled(),
  );
  expect(screen.getByText("Direct sign-in enabled for new@example.test")).toBeInTheDocument();
  expect(screen.getByLabelText(/I understand that changing/)).not.toBeChecked();
  fireEvent.change(screen.getByLabelText("Your current account password"), {
    target: { value: "account-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Disable direct sign-in" }));
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(1);
  fireEvent.click(screen.getByLabelText(/I understand that changing/));
  fireEvent.click(screen.getByRole("button", { name: "Disable direct sign-in" }));
  await waitFor(() => expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(2));
  const writes = request.mock.calls.filter((call) => call[2]?.method);
  expect(JSON.parse(String(writes[0]?.[2]?.body)).expected_revision).toBe(2);
  expect(JSON.parse(String(writes[1]?.[2]?.body)).expected_revision).toBe(3);
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  expect(
    JSON.stringify(client.getQueryData(client.getQueryCache().getAll()[0]!.queryKey)),
  ).not.toMatch(/account-secret|profile-secret/);
});

it("blocks further writes until a failed readback succeeds and is reviewed again", async () => {
  request.mockResolvedValueOnce({
    profile_id: "reader",
    configured: true,
    login_email: "reader@example.test",
    credential_revision: 2,
  });
  request.mockRejectedValueOnce(new Error("Connection lost"));
  request.mockRejectedValueOnce(new Error("Readback unavailable"));
  setup();
  await selectReader();
  fireEvent.click(screen.getByRole("button", { name: "Disable direct sign-in" }));
  await screen.findByText("Readback unavailable");
  expect(screen.queryByRole("form")).toBeNull();
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(1);
  request.mockResolvedValueOnce({
    profile_id: "reader",
    configured: false,
    login_email: "",
    credential_revision: 3,
  });
  fireEvent.click(screen.getByRole("button", { name: "Reload credential status" }));
  await screen.findByRole("form");
  expect(screen.getByText("Direct sign-in is disabled for this profile.")).toBeInTheDocument();
  expect(screen.getByLabelText("Your current account password")).toHaveValue("");
  expect(screen.getByLabelText(/I understand that changing/)).not.toBeChecked();
});

it("discards confirmation and secrets if a background read changes the displayed revision", async () => {
  const { client } = setup();
  await selectReader();
  fireEvent.change(screen.getByLabelText("New profile password"), {
    target: { value: "profile-secret" },
  });
  request.mockResolvedValueOnce({
    profile_id: "reader",
    configured: true,
    login_email: "updated@example.test",
    credential_revision: 3,
  });
  await act(async () => {
    await client.invalidateQueries({ queryKey: ["profile-credential-status"] });
  });
  await screen.findByText("Direct sign-in enabled for updated@example.test");
  expect(screen.getByLabelText("Your current account password")).toHaveValue("");
  expect(screen.getByLabelText("New profile password")).toHaveValue("");
  expect(screen.getByLabelText(/I understand that changing/)).not.toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "Disable direct sign-in" }));
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(0);
});
