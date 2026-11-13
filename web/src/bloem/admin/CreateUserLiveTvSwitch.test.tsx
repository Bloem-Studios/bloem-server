// @vitest-environment jsdom
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { setAccessToken, setProfileId, setProfileToken } from "@/api/client";
import { CreateUserDialogContent } from "@/components/admin/users/CreateUserForm";
import { Dialog } from "@/components/ui/dialog";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";
import { stubRadixDom } from "@/pages/adminPage.test-support";
import { POLICY_DEFAULTS } from "@/test/policyDefaults";

stubRadixDom();

const mocks = vi.hoisted(() => ({ create: vi.fn() }));

vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ user: { id: 1 } }) }));
vi.mock("@/hooks/queries/admin/users", () => ({
  useAdminPolicyDefaults: () => ({ data: POLICY_DEFAULTS }),
  useViewerIsOwner: () => true,
  useAdminUserCapabilities: () => ({ data: { available: true, default_profile: true } }),
  useCreateUser: () => ({ mutateAsync: mocks.create, reset: vi.fn(), isPending: false }),
}));
vi.mock("@/hooks/queries/admin/libraries", () => ({
  useAdminLibraries: () => ({ data: [] }),
}));
vi.mock("@/hooks/queries/admin/accessGroups", () => ({
  useAccessGroups: () => ({ data: [], isSuccess: true }),
}));
// Silo's server-wide downloads notice (#4087) reads the server settings.
vi.mock("@/hooks/queries/admin/settings", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/hooks/queries/admin/settings")>()),
  useAdminServerSettings: () => ({ data: undefined }),
}));

beforeEach(() => {
  setAccessToken("account");
  setProfileId("owner");
  setProfileToken(null);
  mocks.create.mockReset().mockResolvedValue({ id: 11 });
});

async function createAccount(turnOnLiveTv: boolean): Promise<string[]> {
  const user = userEvent.setup();
  render(
    <Dialog open>
      <CreateUserDialogContent onClose={vi.fn()} onBusy={vi.fn()} />
    </Dialog>,
  );
  const dialog = await screen.findByRole("dialog");
  await user.type(within(dialog).getByLabelText("Username"), "newbie");
  await user.type(within(dialog).getByLabelText("Email"), "newbie@example.test");
  await user.type(within(dialog).getByLabelText(/^Password/), "a-long-password");

  await user.click(within(dialog).getByRole("tab", { name: "Access" }));
  const liveTv = within(dialog).getByRole("switch", { name: "Watch Live TV" });
  expect(liveTv).not.toBeChecked();
  if (turnOnLiveTv) {
    await user.click(liveTv);
    expect(liveTv).toBeChecked();
  }

  await user.click(within(dialog).getByRole("button", { name: /save/i }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledTimes(1));
  return mocks.create.mock.calls[0]![0].body.permissions as string[];
}

describe("Bloem's Live TV switch in Silo's create-account dialog", () => {
  it("sends watch_live_tv in the new account's permissions when turned on", async () => {
    expect(await createAccount(true)).toContain(PERMISSION_WATCH_LIVE_TV);
  });

  it("leaves watch_live_tv out when the switch stays off", async () => {
    expect(await createAccount(false)).not.toContain(PERMISSION_WATCH_LIVE_TV);
  });
});
