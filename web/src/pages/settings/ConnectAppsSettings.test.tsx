import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
vi.mock("@/hooks/useAuth", () => ({ useAuth: () => ({ user: { username: "household" } }) }));
import ConnectAppsSettings from "./ConnectAppsSettings";

describe("native connection instructions", () => {
  it("shows the native server and account without protocol setup", () => {
    render(<ConnectAppsSettings />);
    expect(screen.getByText(window.location.origin)).toBeInTheDocument();
    expect(screen.getByText("household")).toBeInTheDocument();
    expect(screen.queryByText(/Jellyfin|Audiobookshelf/i)).not.toBeInTheDocument();
    expect(screen.getByText(/same sign-in method/)).toBeInTheDocument();
  });
});
