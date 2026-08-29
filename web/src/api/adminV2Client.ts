import { getAccessToken, type RequestPolicy } from "./client";
import { randomUUID } from "../lib/uuid";
import type {
  AdminContextFailure,
  AdminContextKey,
  AdminContextSessionResponse,
  AdminContextSummary,
  ApiError,
} from "./types";

interface AdminRequestAuthority {
  token: string;
  key: AdminContextKey;
  generation: number;
  controller: AbortController;
}

const INVALIDATING_CONTEXT_ERRORS = new Set([
  "tenant_session_required",
  "authorization_state_stale",
  "insufficient_platform_authority",
  "organization_suspended",
]);

let adminRequestAuthority: AdminRequestAuthority | null = null;
let adminContextGeneration = 0;
let contextFailureListener: ((failure: AdminContextFailure) => void) | null = null;
const MAX_RETRYABLE_FAILURES = 2;

interface WireOrganization {
  id: string;
  slug: string;
  name: string;
  default: boolean;
  membership_id: string;
  membership_role: string;
  policy_revision: number;
  security_revision: number;
}

interface WireOrganizationsResponse {
  organizations: WireOrganization[];
}

interface WireContextSummary {
  key: AdminContextKey;
  scope: "platform" | "organization";
  organization_id?: string;
  name: string;
  status: "active" | "suspended";
  authority: "platform_admin" | "organization_admin";
  policy_revision?: number;
  security_revision?: number;
}

interface WireSessionResponse {
  access_token: string;
  expires_at: string;
  context: WireContextSummary;
}

export class AdminV2ClientError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields: Record<string, string> = {},
  ) {
    super(message);
    this.name = "AdminV2ClientError";
  }
}

export function setAdminV2Token(token: string | null): void {
  if (token) activateAdminV2Context(token, "platform");
  else clearAdminV2Context();
}

export function activateAdminV2Context(token: string, key: AdminContextKey): void {
  adminRequestAuthority?.controller.abort();
  adminContextGeneration += 1;
  adminRequestAuthority = {
    token,
    key,
    generation: adminContextGeneration,
    controller: new AbortController(),
  };
}

export function clearAdminV2Context(): void {
  adminRequestAuthority?.controller.abort();
  adminContextGeneration += 1;
  adminRequestAuthority = null;
}

export function adminV2QueryKey(
  contextKey: AdminContextKey,
  ...parts: readonly unknown[]
): readonly unknown[] {
  return ["admin-v2", contextKey, ...parts] as const;
}

export function onAdminV2ContextFailure(
  listener: ((failure: AdminContextFailure) => void) | null,
): void {
  contextFailureListener = listener;
}

function requestHeaders(init: RequestInit, token: string): Record<string, string> {
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${token}`);
  if (!(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  return Object.fromEntries(headers.entries());
}

async function parseError(response: Response): Promise<AdminV2ClientError> {
  let body: Partial<ApiError> = {};
  try {
    body = (await response.json()) as Partial<ApiError>;
  } catch {
    // A typed fallback still keeps callers out of response parsing details.
  }
  return new AdminV2ClientError(
    response.status,
    body.error || "unknown",
    body.message || response.statusText || `Request failed (${response.status})`,
    body.fields ?? {},
  );
}

async function readResponse<T>(response: Response): Promise<T> {
  if (!response.ok) throw await parseError(response);
  if (response.status === 204 || response.status === 205) return undefined as T;
  const text = await response.text();
  return text.trim() ? (JSON.parse(text) as T) : (undefined as T);
}

export async function adminV2Api<T>(
  path: string,
  init: RequestInit = {},
  policy: RequestPolicy = safeMethod(init.method) ? "safe" : "none",
): Promise<T> {
  const authority = adminRequestAuthority;
  if (!authority) {
    throw new AdminV2ClientError(
      401,
      "tenant_session_required",
      "Select an administrative context first.",
    );
  }
  const signal = init.signal
    ? AbortSignal.any([authority.controller.signal, init.signal])
    : authority.controller.signal;
  const headers = requestHeaders(init, authority.token);
  if (policy === "idempotentLifecycle" && !new Headers(headers).has("Idempotency-Key")) {
    headers["idempotency-key"] = randomUUID();
  }
  let response: Response;
  let refreshes = 0;
  let retryableFailures = 0;
  for (;;) {
    try {
      response = await fetch(`/api/v2/admin${path.startsWith("/") ? path : `/${path}`}`, {
        ...init,
        headers,
        signal,
      });
    } catch (error) {
      if (!canRetryAdminTransport(policy, retryableFailures, error)) throw error;
      retryableFailures += 1;
      continue;
    }
    assertCurrentAuthority(authority);
    if (response.status === 401 && policy === "idempotentLifecycle" && refreshes === 0) {
      refreshes += 1;
      if (await refreshAdminAuthority(authority)) {
        headers.authorization = `Bearer ${authority.token}`;
        continue;
      }
    }
    if (
      policy !== "none" &&
      retryableFailures < MAX_RETRYABLE_FAILURES &&
      (response.status === 429 || response.status === 503)
    ) {
      retryableFailures += 1;
      continue;
    }
    break;
  }
  if (!response.ok) {
    const error = await parseError(response);
    assertCurrentAuthority(authority);
    if (INVALIDATING_CONTEXT_ERRORS.has(error.code)) {
      clearAdminV2Context();
      contextFailureListener?.({ code: error.code, message: error.message });
    }
    throw error;
  }
  const result = await readResponse<T>(response);
  assertCurrentAuthority(authority);
  return result;
}

function safeMethod(method?: string): boolean {
  const normalized = (method ?? "GET").toUpperCase();
  return normalized === "GET" || normalized === "HEAD" || normalized === "OPTIONS";
}

function canRetryAdminTransport(policy: RequestPolicy, failures: number, error: unknown): boolean {
  return (
    policy !== "none" &&
    failures < MAX_RETRYABLE_FAILURES &&
    !(error instanceof DOMException && error.name === "AbortError")
  );
}

async function refreshAdminAuthority(authority: AdminRequestAuthority): Promise<boolean> {
  const accountToken = getAccessToken();
  if (!accountToken) return false;
  try {
    const session = await mintAdminContextSession(
      authority.key,
      accountToken,
      authority.controller.signal,
    );
    assertCurrentAuthority(authority);
    if (session.context.key !== authority.key) return false;
    authority.token = session.accessToken;
    return true;
  } catch {
    return false;
  }
}

function assertCurrentAuthority(authority: AdminRequestAuthority): void {
  if (
    authority.controller.signal.aborted ||
    adminRequestAuthority?.generation !== authority.generation ||
    adminRequestAuthority.key !== authority.key
  ) {
    throw new DOMException("Administrative context changed during request.", "AbortError");
  }
}

async function accountV2Api<T>(path: string, accountToken: string, init: RequestInit = {}) {
  const response = await fetch(`/api/v2${path}`, {
    ...init,
    headers: requestHeaders(init, accountToken),
  });
  return readResponse<T>(response);
}

export async function fetchAdminOrganizations(
  accountToken = getAccessToken(),
  signal?: AbortSignal,
): Promise<WireOrganization[]> {
  if (!accountToken) {
    throw new AdminV2ClientError(401, "unauthorized", "Authentication required.");
  }
  const response = await accountV2Api<WireOrganizationsResponse>("/organizations", accountToken, {
    signal,
  });
  return response.organizations;
}

function mapContext(context: WireContextSummary): AdminContextSummary {
  return {
    key: context.key,
    scope: context.scope,
    organizationId: context.organization_id,
    name: context.name,
    status: context.status,
    authority: context.authority,
    policyRevision: context.policy_revision ?? 0,
    securityRevision: context.security_revision ?? 0,
  };
}

export async function mintAdminContextSession(
  key: AdminContextKey,
  accountToken = getAccessToken(),
  signal?: AbortSignal,
): Promise<AdminContextSessionResponse> {
  if (!accountToken) {
    throw new AdminV2ClientError(401, "unauthorized", "Authentication required.");
  }
  const organizationId = key.startsWith("organization:")
    ? key.slice("organization:".length)
    : undefined;
  const body = organizationId
    ? { scope: "organization", organization_id: organizationId }
    : { scope: "platform" };
  const response = await accountV2Api<WireSessionResponse>("/admin/session", accountToken, {
    method: "POST",
    body: JSON.stringify(body),
    signal,
  });
  return {
    accessToken: response.access_token,
    expiresAt: response.expires_at,
    context: mapContext(response.context),
  };
}

export type { WireOrganization as AdminOrganizationMembership };
