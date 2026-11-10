import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import BloemProfilePasswordsSettings, {
  PROFILE_PASSWORDS_KEY,
} from "./BloemProfilePasswordsSettings";
import { fakeSettingsForm } from "./settingsForm.test-support";

const useSettingsFormMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

describe("BloemProfilePasswordsSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    useSettingsFormMock.mockReset();
  });

  it("saves the switch and then refreshes the Bloem capabilities", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const invalidate = vi.spyOn(client, "invalidateQueries");
    const clean = fakeSettingsForm({ [PROFILE_PASSWORDS_KEY]: "false" });
    useSettingsFormMock.mockReturnValue(clean);
    const page = () => (
      <QueryClientProvider client={client}>
        <BloemProfilePasswordsSettings />
      </QueryClientProvider>
    );
    const view = render(page());

    expect(useSettingsFormMock).toHaveBeenCalledWith({ keys: [PROFILE_PASSWORDS_KEY] });
    const toggle = screen.getByRole("switch", { name: "Profile passwords" });
    expect(toggle).not.toBeChecked();
    await userEvent.click(toggle);
    expect(clean.setValue).toHaveBeenCalledWith(PROFILE_PASSWORDS_KEY, "true");

    // The form now holds the staged value, so the save bar offers to save it.
    let saved = false;
    const save = vi.fn(async () => {
      saved = true;
    });
    useSettingsFormMock.mockReturnValue(
      fakeSettingsForm(
        { [PROFILE_PASSWORDS_KEY]: "true" },
        { dirty: [PROFILE_PASSWORDS_KEY], persisted: { [PROFILE_PASSWORDS_KEY]: "false" }, save },
      ),
    );
    view.rerender(page());
    expect(screen.getByRole("switch", { name: "Profile passwords" })).toBeChecked();
    invalidate.mockImplementation(async () => {
      // The probe is refreshed only after the setting is written.
      expect(saved).toBe(true);
    });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ["bloem-capabilities"] }),
    );
    expect(save).toHaveBeenCalledOnce();
  });
});
