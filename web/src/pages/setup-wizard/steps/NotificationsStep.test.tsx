import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsStep } from "./NotificationsStep";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver;

const useSettingsFormMock = vi.fn();
const useWizardContextMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

vi.mock("../WizardContext", () => ({
  useWizardContext: (...args: unknown[]) => useWizardContextMock(...args),
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const defaultValues: Record<string, string> = {
  "notifications.apple_push_delivery_enabled": "true",
  "notifications.android_push_delivery_enabled": "true",
};

function mockStep(values: Record<string, string> = {}, dirtyCount = 0) {
  const formValues = { ...defaultValues, ...values };
  const markDone = vi.fn();
  const save = vi.fn().mockResolvedValue(undefined);
  const setValue = vi.fn((key: string, value: string) => {
    formValues[key] = value;
  });
  useWizardContextMock.mockReturnValue({ markDone });
  useSettingsFormMock.mockReturnValue({
    isLoading: false,
    getValue: (key: string) => formValues[key] ?? "",
    setValue,
    dirtyCount,
    dirtyKeys: [],
    save,
    discard: vi.fn(),
    isSaving: false,
  });
  return { markDone, save, setValue };
}

describe("NotificationsStep", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows push enabled by default with the privacy disclosure", () => {
    mockStep();
    render(<NotificationsStep />);

    expect(screen.getByRole("switch", { name: "Mobile push notifications" })).toBeChecked();
  });

  it("shows push off when the server has it disabled", () => {
    mockStep({
      "notifications.apple_push_delivery_enabled": "false",
      "notifications.android_push_delivery_enabled": "false",
    });
    render(<NotificationsStep />);

    expect(screen.getByRole("switch", { name: "Mobile push notifications" })).not.toBeChecked();
    expect(screen.getByText("Privacy disclosure")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "fully open source" })).toHaveAttribute(
      "href",
      expect.stringContaining("github.com"),
    );
  });

  it("writes both platform toggles when the admin turns push off", async () => {
    const { setValue } = mockStep();
    render(<NotificationsStep />);

    await userEvent.click(screen.getByRole("switch", { name: "Mobile push notifications" }));

    expect(setValue).toHaveBeenCalledWith("notifications.apple_push_delivery_enabled", "false");
    expect(setValue).toHaveBeenCalledWith("notifications.android_push_delivery_enabled", "false");
  });

  it("continues without saving when nothing changed", async () => {
    const { markDone, save } = mockStep();
    render(<NotificationsStep />);

    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(save).not.toHaveBeenCalled();
    expect(markDone).toHaveBeenCalledWith("notifications");
  });

  it("saves and marks the step done when the toggle changed", async () => {
    const { markDone, save } = mockStep(
      { "notifications.apple_push_delivery_enabled": "false" },
      2,
    );
    render(<NotificationsStep />);

    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(save).toHaveBeenCalledTimes(1);
    expect(markDone).toHaveBeenCalledWith("notifications");
  });
});
