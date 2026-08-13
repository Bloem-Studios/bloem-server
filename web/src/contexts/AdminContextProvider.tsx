import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Navigate, Outlet, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  AdminV2ClientError,
  activateAdminV2Context,
  clearAdminV2Context,
  fetchAdminOrganizations,
  mintAdminContextSession,
  onAdminV2ContextFailure,
} from "@/api/adminV2Client";
import { getAccessToken } from "@/api/client";
import type {
  AdminContextFailure,
  AdminContextKey,
  AdminContextSummary,
  AdminContextValue,
  User,
} from "@/api/types";
import { useOptionalAuth } from "@/hooks/useAuth";
import { useIsActingAdmin } from "@/hooks/useIsActingAdmin";

const ADMIN_CONTEXT_STORAGE_KEY = "silo-admin-context";
const AdminContext = createContext<AdminContextValue | null>(null);

function storedContextKey(): AdminContextKey | null {
  try {
    const value = window.localStorage.getItem(ADMIN_CONTEXT_STORAGE_KEY);
    return value === "platform" || value?.startsWith("organization:")
      ? (value as AdminContextKey)
      : null;
  } catch {
    return null;
  }
}

function persistContextKey(key: AdminContextKey | null): void {
  try {
    if (key) window.localStorage.setItem(ADMIN_CONTEXT_STORAGE_KEY, key);
    else window.localStorage.removeItem(ADMIN_CONTEXT_STORAGE_KEY);
  } catch {
    // Context selection remains usable when storage is unavailable.
  }
}

function overviewFor(context: AdminContextSummary): string {
  return context.scope === "platform" ? "/admin" : "/admin/organization";
}

function focusPageHeading(): void {
  requestAnimationFrame(() => {
    const heading = document.querySelector<HTMLElement>("#main-content h1, main h1, h1");
    heading?.focus({ preventScroll: true });
  });
}

function failureFrom(error: unknown): AdminContextFailure {
  if (error instanceof AdminV2ClientError) return { code: error.code, message: error.message };
  return { code: "tenant_unavailable", message: "Administrative context is unavailable." };
}

export function AdminContextProvider({
  children,
  user,
  platformAuthority,
}: {
  children: ReactNode;
  user?: Pick<User, "id" | "role"> | null;
  platformAuthority?: boolean;
}) {
  const authUser = useOptionalAuth()?.user;
  const actingAdmin = useIsActingAdmin();
  const currentUser = user === undefined ? authUser : user;
  const canUsePlatform =
    platformAuthority ?? (user === undefined ? actingAdmin : user?.role === "admin");
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [available, setAvailable] = useState<AdminContextSummary[]>([]);
  const [active, setActive] = useState<AdminContextSummary | null>(null);
  const [switching, setSwitching] = useState(true);
  const [failure, setFailure] = useState<AdminContextFailure | null>(null);
  const activeRef = useRef<AdminContextSummary | null>(null);
  const transitionRef = useRef(0);
  const controllerRef = useRef<AbortController | null>(null);

  useEffect(() => {
    activeRef.current = active;
  }, [active]);

  const clearContext = useCallback(
    (reason?: AdminContextFailure) => {
      transitionRef.current += 1;
      controllerRef.current?.abort();
      const previous = activeRef.current;
      clearAdminV2Context();
      setActive(null);
      activeRef.current = null;
      setSwitching(false);
      setFailure(reason ?? null);
      persistContextKey(null);
      if (previous) {
        void queryClient.cancelQueries({ queryKey: ["admin-v2", previous.key] });
        queryClient.removeQueries({ queryKey: ["admin-v2", previous.key] });
      }
      if (reason) navigate("/admin/context", { replace: true });
    },
    [navigate, queryClient],
  );

  const switchContext = useCallback(
    async (key: AdminContextKey, beforeNavigate?: () => void) => {
      const target = available.find((candidate) => candidate.key === key);
      if (!target) {
        const reason = { code: "context_unavailable", message: "That context is unavailable." };
        setFailure(reason);
        throw new AdminV2ClientError(403, reason.code, reason.message);
      }

      const transition = ++transitionRef.current;
      controllerRef.current?.abort();
      const controller = new AbortController();
      controllerRef.current = controller;
      const previous = activeRef.current;
      setSwitching(true);
      setFailure(null);
      if (previous) {
        await queryClient.cancelQueries({ queryKey: ["admin-v2", previous.key] });
        queryClient.removeQueries({ queryKey: ["admin-v2", previous.key] });
      }
      clearAdminV2Context();
      setActive(null);
      activeRef.current = null;

      try {
        const session = await mintAdminContextSession(key, getAccessToken(), controller.signal);
        if (transition !== transitionRef.current || controller.signal.aborted) return;
        activateAdminV2Context(session.accessToken, session.context.key);
        setActive(session.context);
        activeRef.current = session.context;
        persistContextKey(session.context.key);
        setSwitching(false);
        beforeNavigate?.();
        navigate(overviewFor(session.context), { replace: true });
        focusPageHeading();
      } catch (error) {
        if (controller.signal.aborted || transition !== transitionRef.current) return;
        const reason = failureFrom(error);
        setSwitching(false);
        setFailure(reason);
        persistContextKey(null);
        navigate("/admin/context", { replace: true });
        throw error;
      }
    },
    [available, navigate, queryClient],
  );

  const handleContextFailure = useCallback(
    (reason: AdminContextFailure) => {
      const previous = activeRef.current;
      const transition = ++transitionRef.current;
      controllerRef.current?.abort();
      const controller = new AbortController();
      controllerRef.current = controller;
      clearAdminV2Context();
      setActive(null);
      activeRef.current = null;
      setFailure(reason);
      persistContextKey(null);
      if (previous) {
        void queryClient.cancelQueries({ queryKey: ["admin-v2", previous.key] });
        queryClient.removeQueries({ queryKey: ["admin-v2", previous.key] });
      }

      let nextAvailable = available;
      if (reason.code === "insufficient_platform_authority") {
        nextAvailable = available.filter((context) => context.scope !== "platform");
      } else if (reason.code === "organization_suspended" && previous) {
        nextAvailable = available.filter((context) => context.key !== previous.key);
      }
      setAvailable(nextAvailable);

      const recovery =
        reason.code === "tenant_session_required"
          ? nextAvailable.find((context) => context.key === previous?.key)
          : reason.code === "insufficient_platform_authority"
            ? nextAvailable.find((context) => context.scope === "organization")
            : reason.code === "organization_suspended"
              ? nextAvailable[0]
              : undefined;
      if (!recovery) {
        setSwitching(false);
        navigate("/admin/context", { replace: true });
        return;
      }

      setSwitching(true);
      void mintAdminContextSession(recovery.key, getAccessToken(), controller.signal)
        .then((session) => {
          if (controller.signal.aborted || transition !== transitionRef.current) return;
          activateAdminV2Context(session.accessToken, session.context.key);
          setActive(session.context);
          activeRef.current = session.context;
          persistContextKey(session.context.key);
          setFailure(null);
          setSwitching(false);
          navigate(overviewFor(session.context), { replace: true });
          focusPageHeading();
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted || transition !== transitionRef.current) return;
          setFailure(failureFrom(error));
          setSwitching(false);
          navigate("/admin/context", { replace: true });
        });
    },
    [available, navigate, queryClient],
  );

  useEffect(() => {
    onAdminV2ContextFailure(handleContextFailure);
    return () => onAdminV2ContextFailure(null);
  }, [handleContextFailure]);

  useEffect(
    () => () => {
      controllerRef.current?.abort();
      clearAdminV2Context();
    },
    [],
  );

  useEffect(() => {
    const transition = ++transitionRef.current;
    const controller = new AbortController();
    controllerRef.current?.abort();
    controllerRef.current = controller;
    setSwitching(true);
    setActive(null);
    activeRef.current = null;
    clearAdminV2Context();

    void fetchAdminOrganizations(getAccessToken(), controller.signal)
      .then(async (organizations) => {
        if (controller.signal.aborted || transition !== transitionRef.current) return;
        const contexts: AdminContextSummary[] = [];
        if (canUsePlatform) {
          contexts.push({
            key: "platform",
            scope: "platform",
            name: "Platform",
            status: "active",
            authority: "platform_admin",
            policyRevision: 0,
            securityRevision: 0,
          });
        }
        for (const organization of organizations) {
          if (!canUsePlatform && organization.membership_role !== "admin") continue;
          contexts.push({
            key: `organization:${organization.id}`,
            scope: "organization",
            organizationId: organization.id,
            name: organization.name,
            status: "active",
            authority: canUsePlatform ? "platform_admin" : "organization_admin",
            policyRevision: organization.policy_revision,
            securityRevision: organization.security_revision,
          });
        }
        setAvailable(contexts);
        const stored = storedContextKey();
        const initial = contexts.find((context) => context.key === stored) ?? contexts[0];
        if (!initial) {
          setSwitching(false);
          return;
        }
        const session = await mintAdminContextSession(
          initial.key,
          getAccessToken(),
          controller.signal,
        );
        if (controller.signal.aborted || transition !== transitionRef.current) return;
        activateAdminV2Context(session.accessToken, session.context.key);
        setActive(session.context);
        activeRef.current = session.context;
        persistContextKey(session.context.key);
        setFailure(null);
        setSwitching(false);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted || transition !== transitionRef.current) return;
        setFailure(failureFrom(error));
        setSwitching(false);
      });

    return () => controller.abort();
  }, [canUsePlatform, currentUser?.id, currentUser?.role]);

  const value = useMemo<AdminContextValue>(
    () => ({ available, active, switching, failure, switchContext, clearContext }),
    [active, available, clearContext, failure, switchContext, switching],
  );
  return <AdminContext.Provider value={value}>{children}</AdminContext.Provider>;
}

export function useAdminContext(): AdminContextValue {
  const value = useContext(AdminContext);
  if (!value) throw new Error("useAdminContext must be used inside AdminContextProvider");
  return value;
}

function ContextGuard({ scope }: { scope: "platform" | "organization" }) {
  const { active, switching, failure } = useAdminContext();
  if (switching) return null;
  if (!active && failure) return <Navigate to="/admin/context" replace />;
  if (active?.scope !== scope) {
    return (
      <Navigate to={active?.scope === "organization" ? "/admin/organization" : "/admin"} replace />
    );
  }
  return <Outlet />;
}

export function PlatformContextGuard() {
  return <ContextGuard scope="platform" />;
}

export function OrganizationContextGuard() {
  return <ContextGuard scope="organization" />;
}
