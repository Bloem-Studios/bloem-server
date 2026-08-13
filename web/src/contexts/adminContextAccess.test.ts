import { describe, expect, it } from "vitest";
import { canRenderAdminShell } from "./adminContextAccess";

describe("administrative shell access", () => {
  it("denies the shell after discovery finds no administrative context", () => {
    expect(canRenderAdminShell(null, [], false)).toBe(false);
  });

  it("keeps the shell mounted while an authorized context is being re-minted", () => {
    expect(
      canRenderAdminShell(
        null,
        [
          {
            key: "organization:org-a",
            scope: "organization",
            organizationId: "org-a",
            name: "Org A",
            status: "active",
            authority: "organization_admin",
            policyRevision: 7,
            securityRevision: 11,
          },
        ],
        true,
      ),
    ).toBe(true);
  });
});
