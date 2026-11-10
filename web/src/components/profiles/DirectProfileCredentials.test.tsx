// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { nativeApiWithProfileRequestContext } from "@/api/bloemClient";
import { ApiClientError } from "@/api/client";
import { DirectProfileCredentials } from "./DirectProfileCredentials";
const identity = vi.hoisted(() => ({
  generation: 1,
  primary: true,
  tokens: ["profile_credential_management_v1"],
}));
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
  useBloemCapabilities: () => ({ data: { feature_tokens: identity.tokens } }),
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
  identity.tokens = ["profile_credential_management_v1"];
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
  fireEvent.change(screen.getByLabelText("Your account password"), {
    target: { value: "account-secret" },
  });
  fireEvent.click(screen.getByLabelText(/I understand this signs the profile out/));
}
it("saves credentials without retries or secrets in the query/mutation caches", async () => {
  const { client } = setup();
  await selectReader();
  fireEvent.change(screen.getByLabelText("New password for the profile"), {
    target: { value: "profile-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save sign-in" }));
  await screen.findByText(/Sign-in saved/);
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
  expect(screen.getByLabelText("Your account password")).toHaveValue("");
  expect(screen.getByLabelText("New password for the profile")).toHaveValue("");
  expect(client.getMutationCache().getAll()).toHaveLength(0);
  expect(
    JSON.stringify(
      client
        .getQueryCache()
        .getAll()
        .map((query) => query.state.data),
    ),
  ).not.toMatch(/account-secret|profile-secret/);
  expect(screen.getByText(/website still uses the account sign-in/)).toBeInTheDocument();
});
it("disables credentials with explicit confirmation but no replacement password", async () => {
  setup();
  await selectReader();
  fireEvent.click(screen.getByRole("button", { name: "Turn off its sign-in" }));
  await screen.findByText(/own sign-in is off/);
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
  fireEvent.click(screen.getByRole("button", { name: "Turn off its sign-in" }));
  await screen.findByText("Connection lost");
  await waitFor(() => expect(request.mock.calls.filter((call) => !call[2]?.method).length).toBe(2));
  expect(screen.getByLabelText("Your account password")).toHaveValue("");
});
it("discards the selected target and password draft on login generation changes", async () => {
  const view = setup();
  await selectReader();
  identity.generation++;
  view.refresh();
  expect(screen.getByLabelText("Profile")).toHaveValue("");
  expect(screen.queryByLabelText("Your account password")).toBeNull();
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(0);
});
it("says profile passwords are off while the server does not advertise them", () => {
  identity.tokens = [];
  setup();
  expect(screen.getByText(/Profile passwords are turned off on this server/)).toBeInTheDocument();
  expect(screen.queryByLabelText("Profile")).toBeNull();
  expect(request).not.toHaveBeenCalled();
});
it("shows the server's message when profile passwords were turned off before saving", async () => {
  request.mockImplementation(async (_path, _scope, options) => {
    if (options?.method)
      throw new ApiClientError(
        403,
        "profile_passwords_disabled",
        "Profile passwords are turned off on this server",
      );
    return {
      profile_id: "reader",
      configured: true,
      login_email: "reader@example.test",
      credential_revision: 2,
    };
  });
  setup();
  await selectReader();
  fireEvent.change(screen.getByLabelText("New password for the profile"), {
    target: { value: "profile-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save sign-in" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Profile passwords are turned off on this server",
  );
  expect(screen.getByLabelText("New password for the profile")).toHaveValue("");
});
it("does not expose household credential controls on a non-primary viewer profile", () => {
  identity.primary = false;
  setup();
  expect(screen.queryByText("Separate sign-in for a profile")).toBeNull();
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
  fireEvent.change(screen.getByLabelText("New password for the profile"), {
    target: { value: "profile-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Turn off its sign-in" }));
  await screen.findByText(/Credentials changed/);
  await waitFor(() => expect(reads).toBe(2));
  expect(screen.getByLabelText("Your account password")).toHaveValue("");
  expect(screen.getByLabelText("New password for the profile")).toHaveValue("");
  expect(screen.getByLabelText(/I understand this signs the profile out/)).not.toBeChecked();
  expect(screen.getByRole("button", { name: "Turn off its sign-in" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Saving…" })).toBeDisabled();
  fireEvent.submit(screen.getByRole("form"));
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(1);

  await act(async () =>
    finishReadback({ ...initial, credential_revision: 3, login_email: "new@example.test" }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Turn off its sign-in" })).toBeEnabled(),
  );
  expect(screen.getByText("This profile signs in as new@example.test")).toBeInTheDocument();
  expect(screen.getByLabelText(/I understand this signs the profile out/)).not.toBeChecked();
  fireEvent.change(screen.getByLabelText("Your account password"), {
    target: { value: "account-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Turn off its sign-in" }));
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(1);
  fireEvent.click(screen.getByLabelText(/I understand this signs the profile out/));
  fireEvent.click(screen.getByRole("button", { name: "Turn off its sign-in" }));
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
  fireEvent.click(screen.getByRole("button", { name: "Turn off its sign-in" }));
  await screen.findByText("Readback unavailable");
  expect(screen.queryByRole("form")).toBeNull();
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(1);
  request.mockResolvedValueOnce({
    profile_id: "reader",
    configured: false,
    login_email: "",
    credential_revision: 3,
  });
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await screen.findByRole("form");
  expect(screen.getByText("This profile has no sign-in of its own.")).toBeInTheDocument();
  expect(screen.getByLabelText("Your account password")).toHaveValue("");
  expect(screen.getByLabelText(/I understand this signs the profile out/)).not.toBeChecked();
});

it("discards confirmation and secrets if a background read changes the displayed revision", async () => {
  const { client } = setup();
  await selectReader();
  fireEvent.change(screen.getByLabelText("New password for the profile"), {
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
  await screen.findByText("This profile signs in as updated@example.test");
  expect(screen.getByLabelText("Your account password")).toHaveValue("");
  expect(screen.getByLabelText("New password for the profile")).toHaveValue("");
  expect(screen.getByLabelText(/I understand this signs the profile out/)).not.toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: "Turn off its sign-in" }));
  expect(request.mock.calls.filter((call) => call[2]?.method)).toHaveLength(0);
});
