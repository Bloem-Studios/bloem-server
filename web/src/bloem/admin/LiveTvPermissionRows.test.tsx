// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import type { ProfileRequestContextSnapshot } from "@/api/client";
import type { AccessGroup, AdminUser, Library, UpdateUserRequest } from "@/api/types";
import type { AdminUserEditor } from "@/api/v2/adminUsers";
import { PERMISSION_WATCH_LIVE_TV } from "@/lib/bloemPermissions";
import { stubRadixDom } from "@/pages/admin-users/detail/adminUserTab.test-support";
import { AccessTab } from "@/pages/admin-users/detail/access/AccessTab";
import { CardEditingProvider } from "@/pages/admin-users/detail/cardEditing";
import { POLICY_DEFAULTS } from "@/test/policyDefaults";

const mocks = vi.hoisted(() => ({ update: vi.fn(), user: null as AdminUser | null }));
const CONTEXT = { profileId: "owner" } as unknown as ProfileRequestContextSnapshot;

vi.mock("@/api/v2/adminUsers", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/v2/adminUsers")>()),
  getAdminUser: async () => ({ user: mocks.user!, etag: '"read"', profileContext: CONTEXT }),
}));
vi.mock("@/hooks/queries/admin/users", () => ({
  useAdminPolicyDefaults: () => ({ data: POLICY_DEFAULTS }),
  useViewerIsOwner: () => false,
  useAdminUserCapabilities: () => ({
    data: { available: true, account_downloads: true, request_usage: false },
  }),
  useUpdateUser: () => ({ mutateAsync: mocks.update, isPending: false }),
}));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ user: { id: 1 } }) }));
vi.mock("@/hooks/queries/admin/history", () => ({
  useAdminUserProfiles: () => ({ data: [{ id: "p1", name: "Main" }] }),
}));
vi.mock("@/hooks/queries/admin/userActivity", () => ({
  useAdminUserRequestUsage: () => ({ data: undefined }),
}));
vi.mock("@/hooks/queries/admin/requests", () => ({
  useRequestSettings: () => ({
    data: {
      requests_enabled: true,
      global_max_requests: 50,
      global_window_days: 7,
      global_auto_approval_enabled: true,
      force_dual_quality: false,
      updated_at: "2026-01-01T00:00:00Z",
    },
    isError: false,
    refetch: vi.fn(),
  }),
  useRequestUserLimit: () => ({
    data: { user_id: 7, limit_mode: "inherit", approval_mode: "inherit", etag: '"limit"' },
    isError: false,
    isLoading: false,
    refetch: vi.fn(),
  }),
  useRequestGroupLimit: () => ({ data: undefined, isError: false, refetch: vi.fn() }),
  useUpdateRequestUserLimit: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/hooks/queries/admin/accessGroups", () => ({
  useAccessGroups: () => ({ data: [] as AccessGroup[] }),
}));
vi.mock("@/hooks/queries/admin/libraries", () => ({
  useAdminLibraries: () => ({
    data: [{ id: 1, name: "Movies", enabled: true, type: "movies" }] as Library[],
  }),
}));

const USER: AdminUser = {
  id: 7,
  username: "taylor",
  email: "taylor@example.test",
  role: "user",
  permissions: [],
  enabled: true,
  library_ids: null,
  access_group_id: null,
  max_playback_quality: null,
  max_streams: null,
  max_transcodes: null,
  max_remote_stream_bitrate_kbps: null,
  max_local_stream_bitrate_kbps: null,
  transcode_allowed: null,
  audio_transcode_allowed: null,
  max_profiles: null,
  download_allowed: null,
  download_transcode_allowed: null,
  requests_allowed: null,
  password_login: true,
  password_change_required: false,
  is_owner: false,
  break_glass: false,
  effective_policy: {
    library_ids: null,
    max_playback_quality: "",
    max_streams: 0,
    max_transcodes: 0,
    max_remote_stream_bitrate_kbps: 0,
    max_local_stream_bitrate_kbps: 0,
    max_profiles: 4,
    transcode_allowed: true,
    audio_transcode_allowed: true,
    download_allowed: true,
    download_transcode_allowed: false,
    requests_allowed: true,
    permissions: [],
  },
  created_at: "2026-07-01T12:00:00Z",
  updated_at: "2026-07-01T12:00:00Z",
};

beforeEach(() => {
  stubRadixDom();
  mocks.update.mockReset().mockResolvedValue(undefined);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("counts and saves the Live TV permission in the account's library access card", async () => {
  const ui = userEvent.setup();
  mocks.user = USER;
  const editor: AdminUserEditor = { user: USER, etag: '"cached"', profileContext: CONTEXT };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <CardEditingProvider>
          <AccessTab user={USER} editor={editor} manageable policyManageable available />
        </CardEditingProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );

  await ui.click(screen.getByRole("button", { name: "Edit Library access" }));
  const card = screen.getByRole("region", { name: "Library access" });
  await ui.click(within(card).getByRole("switch", { name: "Watch Live TV" }));
  expect(within(card).getByText("1 unsaved change · Watch Live TV")).toBeInTheDocument();

  await ui.click(within(card).getByRole("button", { name: "Save" }));
  await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
  const body = (mocks.update.mock.calls[0]![0] as { body: UpdateUserRequest }).body;
  expect(body.permissions).toEqual([PERMISSION_WATCH_LIVE_TV]);
});
