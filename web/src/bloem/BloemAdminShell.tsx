// Bloem administrative-context shell components mounted by the Silo-owned App.tsx.
import type { ReactNode } from "react";
import { Navigate } from "react-router";
import { useAuth } from "@/hooks/useAuth";
import { useIsActingAdmin } from "@/hooks/useIsActingAdmin";
import { AdminContextProvider, useAdminContext } from "@/contexts/AdminContextProvider";
import { canRenderAdminShell } from "@/contexts/adminContextAccess";

function AdminContextRoot({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const actingAdmin = useIsActingAdmin();
  return (
    <AdminContextProvider user={user} platformAuthority={actingAdmin}>
      {children}
    </AdminContextProvider>
  );
}

function RequireAdminContext({ children }: { children: ReactNode }) {
  const { active, available, switching } = useAdminContext();
  if (switching && !active) {
    return (
      <div className="p-8" role="status" aria-live="polite">
        Loading administrative context…
      </div>
    );
  }
  if (!canRenderAdminShell(active, available, switching)) return <Navigate to="/" replace />;
  return <>{children}</>;
}

/** Replaces Silo's RequireAdmin: admin routes render inside an administrative context. */
export function RequireAdmin({ children }: { children: ReactNode }) {
  return (
    <AdminContextRoot>
      <RequireAdminContext>{children}</RequireAdminContext>
    </AdminContextRoot>
  );
}

export function AdminContextSelection() {
  const { failure } = useAdminContext();
  return (
    <section className="admin-page max-w-2xl">
      <h1 className="page-title text-[clamp(2rem,4vw,3rem)]" tabIndex={-1}>
        Choose administrative context
      </h1>
      <p className="text-muted-foreground mt-3">
        Select Platform or an organization from the context control in the navigation.
      </p>
      {failure ? (
        <p
          className="border-destructive/30 bg-destructive/10 text-destructive mt-6 rounded-xl border p-4 text-sm"
          role="alert"
        >
          {failure.message}
        </p>
      ) : null}
    </section>
  );
}

export function AdminContextRedirect() {
  const { active, switching } = useAdminContext();
  if (switching) return null;
  return (
    <Navigate to={active?.scope === "organization" ? "/admin/organization" : "/admin"} replace />
  );
}
