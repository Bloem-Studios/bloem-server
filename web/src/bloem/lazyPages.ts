// Lazily loaded pages used by the Bloem route table (src/bloem/routes.tsx).
import { lazy } from "react";

export const LiveTVWatch = lazy(() => import("@/pages/LiveTVWatch"));
export const AdminAccessGroups = lazy(() => import("@/pages/AdminAccessGroups"));
