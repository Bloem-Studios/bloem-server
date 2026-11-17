// Bloem: announcements in the notification inbox (NotificationInbox.tsx). The
// harness follows Silo's pages/Notifications.test.tsx.
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import Notifications from "@/pages/Notifications";

const state = vi.hoisted(() => ({
  list: {
    data: undefined as unknown,
    isLoading: false,
    isError: false,
    hasNextPage: false,
    restart: vi.fn(),
  },
}));
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({}) }));
vi.mock("@/api/v2/notifications", () => ({ notificationScope: () => "owner" }));
vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: () => {} }));
vi.mock("@/hooks/queries/notificationWebhooks", () => ({
  useNotificationCapability: () => ({ data: undefined }),
}));
vi.mock("@/hooks/queries/notifications", async (importOriginal) => ({
  formatEpisodeCode: (await importOriginal<typeof import("@/hooks/queries/notifications")>())
    .formatEpisodeCode,
  useNotificationSeries: () => undefined,
  useNotifications: () => state.list,
  useUnreadNotificationCount: () => ({ data: 1 }),
  useMarkAllNotificationsRead: () => ({
    mutateAsync: vi.fn(),
    isPending: false,
    isError: false,
    reset: vi.fn(),
  }),
  useMarkNotificationsRead: () => ({ mutate: vi.fn() }),
  useNotificationPreferences: () => ({ data: undefined, isLoading: false }),
  useUpdateNotificationPreferences: () => ({ mutate: vi.fn() }),
  useSetNotificationSeriesMuted: () => ({ mutate: vi.fn(), isPending: false }),
  useSetManyNotificationSeriesMuted: () => ({ mutate: vi.fn(), isPending: false }),
}));
afterEach(cleanup);

it("shows announcement title, message and severity", () => {
  state.list.data = {
    pages: [
      {
        notifications: [
          {
            id: "notice",
            type: "system.announcement",
            title: "Maintenance tonight",
            body: "Playback resumes at 22:00.",
            severity: "warning",
            created_at: "2026-01-01T00:00:00Z",
            reason_flags: {},
            read_at: null,
          },
        ],
        read_cutoff: "frozen-cutoff",
      },
    ],
  };
  render(
    <MemoryRouter initialEntries={["/notifications"]}>
      <Notifications />
    </MemoryRouter>,
  );
  expect(screen.getByText("Maintenance tonight")).toBeInTheDocument();
  expect(screen.getByText("Playback resumes at 22:00.")).toBeInTheDocument();
  expect(screen.getByText("Warning")).toBeInTheDocument();
});
