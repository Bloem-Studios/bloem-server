// @vitest-environment jsdom
import type { ReactElement } from "react";
import { createRoutesFromElements, matchRoutes, Navigate, Route } from "react-router";
import { describe, expect, it } from "vitest";

import { OrganizationOverviewPage, PeoplePage } from "./lazyPages";
import { bloemAdminContextRoutes } from "./routes";

const routes = createRoutesFromElements(<Route path="/admin/*">{bloemAdminContextRoutes}</Route>);

function leaf(pathname: string) {
  const matches = matchRoutes(routes, pathname);
  return matches?.[matches.length - 1]?.route.element as ReactElement | undefined;
}

describe("organization admin routes", () => {
  it("opens the overview at /admin/organization instead of the catch-all redirect", () => {
    expect(leaf("/admin/organization")?.type).toBe(OrganizationOverviewPage);
    expect(leaf("/admin/organization/")?.type).toBe(OrganizationOverviewPage);
  });

  it("keeps the named pages and redirects unknown organization paths", () => {
    expect(leaf("/admin/organization/people")?.type).toBe(PeoplePage);
    expect(leaf("/admin/organization/nope")?.type).toBe(Navigate);
  });
});
