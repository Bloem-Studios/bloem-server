// @vitest-environment node
// Bloem: the admin navigation in an organization or platform context.

import { describe, expect, it } from "vitest";

import { buildAdminNavSections } from "./adminNavigation";

describe("storage sources navigation", () => {
  const hrefs = (scope: "platform" | "organization") =>
    buildAdminNavSections({ scope }).flatMap((section) =>
      section.entries.flatMap((entry) => ("items" in entry ? entry.items : [entry])),
    );

  it("lists Storage sources for organization admins", () => {
    const item = hrefs("organization").find((i) => i.href === "/admin/organization/storage");
    expect(item?.label).toBe("Storage sources");
  });

  it("keeps it out of the platform navigation, which reaches storage through Plugins", () => {
    const platform = hrefs("platform");
    expect(platform.some((i) => i.href === "/admin/organization/storage")).toBe(false);
    expect(platform.some((i) => i.href === "/admin/plugins")).toBe(true);
  });

  it("does not offer organization admins the platform Plugins page", () => {
    expect(hrefs("organization").some((i) => i.href === "/admin/plugins")).toBe(false);
  });
});
