import { AdminScopedOutlet } from "@/bloem/BloemAdminShell";

// eslint-disable-next-line react-refresh/only-export-components -- the admin layout's react-router, with one component replaced
export * from "react-router";

/**
 * The admin layout's page outlet: Bloem's admin text loads first, and a
 * platform page waits for the platform context (BloemAdminShell.tsx).
 */
export function Outlet() {
  return <AdminScopedOutlet />;
}
