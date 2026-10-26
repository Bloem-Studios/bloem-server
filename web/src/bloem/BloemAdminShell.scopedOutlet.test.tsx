// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import { afterEach, describe, expect, it, vi } from "vitest";

const admin = vi.hoisted(() => ({
  value: null as null | {
    active: { key: string; scope: "platform" | "organization" } | null;
    switching: boolean;
    failure: { message: string } | null;
  },
}));

// The guard's own redirects are covered by AdminContextProvider.test.tsx; this
// pins which admin pages AdminLayout's outlet puts behind it.
vi.mock("@/contexts/AdminContextProvider", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/contexts/AdminContextProvider")>();
  return {
    ...actual,
    useOptionalAdminContext: () => admin.value,
    PlatformContextGuard: () => <p>platform guard</p>,
  };
});

import { AdminScopedOutlet } from "./BloemAdminShell";

function Where() {
  return <p>{`at ${useLocation().pathname}`}</p>;
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/admin/*" element={<AdminScopedOutlet />}>
          <Route index element={<p>dashboard</p>} />
          <Route path="plugins" element={<p>plugins page</p>} />
          <Route path="organization" element={<Where />} />
          <Route path="organization/people" element={<p>people page</p>} />
          <Route path="context" element={<Where />} />
        </Route>
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  );
}

const platform = { key: "platform", scope: "platform" as const };
const organization = { key: "organization:org-1", scope: "organization" as const };

describe("AdminScopedOutlet", () => {
  afterEach(() => {
    cleanup();
    admin.value = null;
  });

  it.each(["/admin", "/admin/plugins", "/admin/unknown"])(
    "puts %s behind the platform guard",
    (path) => {
      admin.value = { active: organization, switching: false, failure: null };
      renderAt(path);
      expect(screen.getByText("platform guard")).toBeInTheDocument();
      expect(screen.queryByText("plugins page")).not.toBeInTheDocument();
      expect(screen.queryByText("dashboard")).not.toBeInTheDocument();
    },
  );

  it.each([
    ["/admin/organization/people", "people page"],
    ["/admin/organization", "at /admin/organization"],
    ["/admin/context", "at /admin/context"],
  ])("leaves %s to its own route", (path, text) => {
    admin.value = { active: platform, switching: false, failure: null };
    renderAt(path);
    expect(screen.getByText(text)).toBeInTheDocument();
    expect(screen.queryByText("platform guard")).not.toBeInTheDocument();
  });

  it("renders the plain outlet outside an administrative context", () => {
    renderAt("/admin/plugins");
    expect(screen.getByText("plugins page")).toBeInTheDocument();
  });
});
