// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, waitFor } from "@testing-library/react";
import { V2ProblemError } from "@/api/v2/request";
import { renderLogin, signInWithPassword } from "./loginPage.test-support";

// Bloem: a sign-in on another organization's domain is refused with a 409
// conflict whose detail names the account's own address. The page shows that
// detail; it must not read it as a disabled account.

const mocks = vi.hoisted(() => ({ login: vi.fn(), toastError: vi.fn() }));
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({
    loading: false,
    setupLoading: false,
    setupRequired: false,
    user: null,
    providers: [{ id: "local", mode: "credentials", display_name: "Local", default: true }],
    login: mocks.login,
  }),
  getBootstrapProfile: vi.fn(),
}));
vi.mock("@/api/v2/request", async () => ({
  ...(await vi.importActual<typeof import("@/api/v2/request")>("@/api/v2/request")),
  v2: vi.fn(async () => ({ enabled: false })),
}));
vi.mock("@/hooks/usePostSignInNavigation", () => ({
  CHANGE_PASSWORD_PATH: "/change-password",
  usePostSignInNavigation: () => vi.fn(async () => undefined),
}));
vi.mock("@/hooks/useServerBranding", () => ({
  useServerBranding: () => ({ serverName: "Main", loginSubtitle: "Sign in" }),
}));
vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: vi.fn() }));
vi.mock("@/components/auth/AuthBackground", () => ({ AuthBackground: () => null }));
vi.mock("sonner", () => ({ toast: { error: mocks.toastError, success: vi.fn() } }));
afterEach(() => {
  cleanup();
  mocks.login.mockReset();
  mocks.toastError.mockReset();
});

it("shows the address an account signs in at when refused on another organization's domain", async () => {
  mocks.login.mockRejectedValue(
    new V2ProblemError("login", {
      type: "https://siloserver.org/docs/api/v2/problems/conflict",
      title: "Conflict",
      status: 409,
      detail: "This account signs in at https://main.domain.com.",
      instance: "urn:silo:request:1",
    }),
  );
  renderLogin();
  signInWithPassword("alice");
  await waitFor(() =>
    expect(mocks.toastError).toHaveBeenCalledWith(
      "This account signs in at https://main.domain.com.",
    ),
  );
});
