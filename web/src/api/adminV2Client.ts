import { getAccessToken } from "./client";
import type {
  AdminContextFailure,
  AdminContextKey,
  AdminContextSessionResponse,
  AdminContextSummary,
  ApiError,
} from "./types";

let adminContextToken: string | null = null;
let contextFailureListener: ((failure: AdminContextFailure) => void) | null = null;

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
  ) {
    super(message);
    this.name = "AdminV2ClientError";
  }
}

export function setAdminV2Token(token: string | null): void {
  adminContextToken = token;
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
  );
}

async function readResponse<T>(response: Response): Promise<T> {
  if (!response.ok) throw await parseError(response);
  if (response.status === 204 || response.status === 205) return undefined as T;
  const text = await response.text();
  return text.trim() ? (JSON.parse(text) as T) : (undefined as T);
}

export async function adminV2Api<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (!adminContextToken) {
    throw new AdminV2ClientError(
      401,
      "tenant_session_required",
      "Select an administrative context first.",
    );
  }
  const response = await fetch(`/api/v2/admin${path.startsWith("/") ? path : `/${path}`}`, {
    ...init,
    headers: requestHeaders(init, adminContextToken),
  });
  if (!response.ok) {
    const error = await parseError(response);
    if (error.code === "authorization_state_stale") {
      adminContextToken = null;
      contextFailureListener?.({ code: error.code, message: error.message });
    }
    throw error;
  }
  return readResponse<T>(response);
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
