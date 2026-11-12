import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ADMIN_NAV_SECTIONS } from "@/lib/adminNavigation";
import { ADMIN_SETTINGS_NAV } from "@/lib/adminSettingsSearch";

import BloemStrmSettings, { STRM_PRIVATE_ADDRESSES_KEY } from "./BloemStrmSettings";
import { fakeSettingsForm } from "./settingsForm.test-support";

const useSettingsFormMock = vi.fn();

vi.mock("@/hooks/useSettingsForm", () => ({
  useSettingsForm: (...args: unknown[]) => useSettingsFormMock(...args),
}));

describe("BloemStrmSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    useSettingsFormMock.mockReset();
  });

  it("stages and saves the private-address switch, off by default", async () => {
    const clean = fakeSettingsForm({});
    useSettingsFormMock.mockReturnValue(clean);
    const view = render(<BloemStrmSettings />);

    expect(STRM_PRIVATE_ADDRESSES_KEY).toBe("bloem.strm.allow_private_addresses");
    expect(useSettingsFormMock).toHaveBeenCalledWith({ keys: [STRM_PRIVATE_ADDRESSES_KEY] });
    const toggle = screen.getByRole("switch", { name: "Allow private addresses" });
    expect(toggle).not.toBeChecked();
    await userEvent.click(toggle);
    expect(clean.setValue).toHaveBeenCalledWith(STRM_PRIVATE_ADDRESSES_KEY, "true");

    const save = vi.fn(async () => {});
    useSettingsFormMock.mockReturnValue(
      fakeSettingsForm(
        { [STRM_PRIVATE_ADDRESSES_KEY]: "true" },
        {
          dirty: [STRM_PRIVATE_ADDRESSES_KEY],
          persisted: { [STRM_PRIVATE_ADDRESSES_KEY]: "false" },
          save,
        },
      ),
    );
    view.rerender(<BloemStrmSettings />);
    expect(screen.getByRole("switch", { name: "Allow private addresses" })).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(save).toHaveBeenCalledOnce();
  });

  it("is a settings page under Libraries, after Silo's library settings", () => {
    expect(ADMIN_SETTINGS_NAV.some((page) => page.id === "strm")).toBe(true);
    const libraries = ADMIN_NAV_SECTIONS.flatMap((section) => section.entries).find(
      (entry) => "items" in entry && entry.id === "libraries",
    );
    const hrefs = libraries && "items" in libraries ? libraries.items.map((item) => item.href) : [];
    const settings = hrefs.indexOf("/admin/settings/library");
    expect(settings).toBeGreaterThanOrEqual(0);
    expect(hrefs[settings + 1]).toBe("/admin/settings/strm");
  });
});
