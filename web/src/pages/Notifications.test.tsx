import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import Notifications from "./Notifications";
vi.mock("@/hooks/useDocumentTitle", () => ({ useDocumentTitle: vi.fn() }));
vi.mock("@/hooks/queries/notifications", () => ({
  useNotifications: () => ({
    data: {
      pages: [
        {
          notifications: [
            {
              id: "notice",
              type: "system.announcement",
              profile_id: "profile",
              reason_flags: {},
              title: "Maintenance tonight",
              body: "Playback resumes at 22:00.",
              severity: "warning",
              created_at: "2026-09-05T10:00:00Z",
              read_at: null,
            },
          ],
        },
      ],
    },
  }),
  useUnreadNotificationCount: () => ({ data: 1 }),
  useMarkNotificationRead: () => ({ mutate: vi.fn() }),
  useMarkAllNotificationsRead: () => ({ mutate: vi.fn() }),
  useNotificationPreferences: () => ({}),
  useUpdateNotificationPreferences: () => ({ mutate: vi.fn() }),
  formatEpisodeCode: () => "",
}));
describe("announcement inbox", () => {
  it("shows an announcement's actual title, full message and severity", () => {
    render(<Notifications />);
    expect(screen.getByText("Maintenance tonight")).toBeInTheDocument();
    expect(screen.getByText("Playback resumes at 22:00.")).toBeInTheDocument();
    expect(screen.getByText("Warning")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Mark as read" })).toBeInTheDocument();
  });
});
