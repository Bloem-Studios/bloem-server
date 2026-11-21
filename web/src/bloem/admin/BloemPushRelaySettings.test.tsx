import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { BLOEM_PUSH_RELAY_REGISTER, BloemPushRelaySettings } from "./BloemPushRelaySettings";

const adminContext = vi.hoisted(() => ({
  value: { active: { scope: "platform", key: "platform" } } as {
    active: { scope: "platform" | "organization"; key: string } | null;
  } | null,
}));
const api = vi.hoisted(() => ({ adminV2Api: vi.fn(), capture: vi.fn() }));
const toasts = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock("@/contexts/AdminContextProvider", () => ({
  useOptionalAdminContext: () => adminContext.value,
}));
vi.mock("@/api/adminV2Client", () => ({
  adminV2Api: api.adminV2Api,
  captureAdminRequestContext: api.capture,
}));
vi.mock("sonner", () => ({ toast: toasts }));

describe("BloemPushRelaySettings", () => {
  beforeEach(() => {
    adminContext.value = { active: { scope: "platform", key: "platform" } };
    api.adminV2Api.mockReset();
    api.capture.mockReset().mockReturnValue({ key: "platform", generation: 3 });
    toasts.success.mockReset();
    toasts.error.mockReset();
  });

  it("registers with Bloem's relay through the platform route", async () => {
    api.adminV2Api.mockResolvedValue({
      relay_url: "https://push.bloem-studios.com",
      deployment_id: "dep-7",
      key_prefix: "cap_v1_x",
      api_key_configured: true,
      expires_at: "2026-12-21T10:00:00Z",
    });
    render(<BloemPushRelaySettings />);
    expect(screen.getByText("Push relay for the Bloem Admin app")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Register again" }));

    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("dep-7"));
    expect(api.adminV2Api).toHaveBeenCalledTimes(1);
    expect(api.adminV2Api).toHaveBeenCalledWith(
      BLOEM_PUSH_RELAY_REGISTER,
      { method: "POST" },
      "none",
      { key: "platform", generation: 3 },
    );
    expect(BLOEM_PUSH_RELAY_REGISTER).toBe("/platform/notifications/push/relay/register");
    expect(api.capture).toHaveBeenCalledWith("platform");
    expect(toasts.success).toHaveBeenCalledWith("Registered with the Bloem Admin app's push relay");
  });

  it("says why a registration failed", async () => {
    api.adminV2Api.mockRejectedValue(new Error("Push relay rejected this deployment"));
    render(<BloemPushRelaySettings />);

    fireEvent.click(screen.getByRole("button", { name: "Register again" }));

    await waitFor(() =>
      expect(toasts.error).toHaveBeenCalledWith(
        "Could not register with the Bloem Admin app's push relay",
        { description: "Push relay rejected this deployment" },
      ),
    );
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Register again" })).toBeEnabled();
  });

  it("shows nothing outside the Platform context", () => {
    adminContext.value = { active: { scope: "organization", key: "organization:org-1" } };
    const { container, rerender } = render(<BloemPushRelaySettings />);
    expect(container).toBeEmptyDOMElement();

    adminContext.value = null;
    rerender(<BloemPushRelaySettings />);
    expect(container).toBeEmptyDOMElement();
    expect(api.adminV2Api).not.toHaveBeenCalled();
  });
});
