import { nativeApiWithProfileRequestContext } from "./bloemClient";
import { captureProfileRequestContext, type ProfileRequestContextSnapshot } from "./client";
import { adminV2Api, type AdminRequestContext } from "./adminV2Client";

export interface BookRequest {
  id: string;
  organization_id: string;
  media_type: "audiobook" | "book";
  title: string;
  author: string;
  isbn?: string;
  asin?: string;
  note?: string;
  status: "pending" | "approved" | "completed";
  outcome: "active" | "declined" | "cancelled";
  outcome_reason?: string;
  requested_by_user_id: number;
  requested_by_profile_id: string;
  library_content_id?: string;
  fulfilment: string;
  revision: number;
  created_at: string;
  updated_at: string;
}
export interface BookRequestInput {
  title: string;
  author: string;
  isbn: string;
  asin: string;
  note: string;
}
export interface BookRequestCapability {
  available: boolean;
  enabled: boolean;
  can_request: boolean;
  auto_approve: boolean;
  unlimited: boolean;
  limit: number;
  used: number;
  remaining: number;
  window_days: number;
  fulfilment_backend: string;
  media_types?: string[];
  manual_fulfilment?: boolean;
}
export type EbookRequestInput = Omit<BookRequestInput, "asin">;
export type EbookRequestAction = "approve" | "decline" | "retry";
export interface EbookRequestIntegrations {
  available: boolean;
  enabled: boolean;
  integrations: { id: string; name: string; library_id?: number }[];
  libraries: { id: number; name: string }[];
  installations: { id: number; name: string; capability_id: string }[];
}
export interface EbookRequestIntegrationInput {
  name: string;
  base_url: string;
  api_key: string;
  installation_id: number;
  capability_id: string;
  allow_http?: boolean;
}
export type BookRequestAction = "approve" | "decline" | "fulfil" | "cancel";
export function bookRequestState(row: BookRequest): string {
  if (row.outcome === "declined") return "Declined";
  if (row.outcome === "cancelled") return "Cancelled";
  if (row.status === "pending") return "Awaiting approval";
  if (row.status === "approved") {
    if (row.media_type === "book") {
      const stages: Record<string, string> = {
        queued: "queued for fulfilment",
        searching: "searching for ebook",
        retrying: "retrying fulfilment",
        awaiting_library: "awaiting library availability",
        failed: "fulfilment failed",
      };
      const stage = Object.hasOwn(stages, row.fulfilment)
        ? stages[row.fulfilment]
        : "awaiting fulfilment";
      return `Approved · ${stage}`;
    }
    return "Approved · awaiting fulfilment";
  }
  if (row.media_type === "book") {
    return row.fulfilment === "library" && row.library_content_id
      ? "In your library"
      : "Awaiting library availability";
  }
  return row.fulfilment === "manual" ? "Fulfilled by an admin" : "In your library";
}
function profileAuthority(context: ProfileRequestContextSnapshot | null) {
  if (!context) throw new Error("Select a profile before opening requests.");
  return context;
}
export const bookRequestsApi = {
  capability: (context = captureProfileRequestContext()) =>
    nativeApiWithProfileRequestContext<BookRequestCapability>(
      "/book-requests/capability",
      profileAuthority(context),
    ),
  mine: (context = captureProfileRequestContext(), offset = 0) =>
    nativeApiWithProfileRequestContext<{ requests: BookRequest[] }>(
      `/book-requests/mine?limit=100&offset=${offset}`,
      profileAuthority(context),
    ),
  create: (input: BookRequestInput, key: string, context: ProfileRequestContextSnapshot | null) =>
    nativeApiWithProfileRequestContext<BookRequest>(
      "/book-requests",
      profileAuthority(context),
      { method: "POST", headers: { "Idempotency-Key": key }, body: JSON.stringify(input) },
      "idempotentLifecycle",
    ),
  cancel: (row: BookRequest, context: ProfileRequestContextSnapshot | null) =>
    nativeApiWithProfileRequestContext<BookRequest>(
      `/book-requests/${encodeURIComponent(row.id)}/cancel`,
      profileAuthority(context),
      { method: "POST", body: JSON.stringify({ expected_revision: row.revision }) },
      "none",
    ),
  adminList: (context: AdminRequestContext | null, offset = 0) =>
    adminV2Api<{ requests: BookRequest[] }>(
      `/organization/book-requests?limit=100&offset=${offset}`,
      {},
      "safe",
      context,
    ),
  adminAction: (
    row: BookRequest,
    action: BookRequestAction,
    reason: string,
    context: AdminRequestContext | null,
  ) =>
    adminV2Api<BookRequest>(
      `/organization/book-requests/${encodeURIComponent(row.id)}/${action}`,
      { method: "POST", body: JSON.stringify({ expected_revision: row.revision, reason }) },
      "none",
      context,
    ),
};

export const ebookRequestsApi = {
  capability: (context = captureProfileRequestContext()) =>
    nativeApiWithProfileRequestContext<BookRequestCapability>(
      "/ebook-requests/capability",
      profileAuthority(context),
    ),
  mine: (context = captureProfileRequestContext(), offset = 0) =>
    nativeApiWithProfileRequestContext<{ requests: BookRequest[] }>(
      `/ebook-requests/mine?limit=100&offset=${offset}`,
      profileAuthority(context),
    ),
  get: (id: string, context = captureProfileRequestContext()) =>
    nativeApiWithProfileRequestContext<BookRequest>(
      `/ebook-requests/${encodeURIComponent(id)}`,
      profileAuthority(context),
    ),
  create: (input: EbookRequestInput, key: string, context: ProfileRequestContextSnapshot | null) =>
    nativeApiWithProfileRequestContext<BookRequest>(
      "/ebook-requests",
      profileAuthority(context),
      { method: "POST", headers: { "Idempotency-Key": key }, body: JSON.stringify(input) },
      "idempotentLifecycle",
    ),
  cancel: (row: BookRequest, context: ProfileRequestContextSnapshot | null) =>
    nativeApiWithProfileRequestContext<BookRequest>(
      `/ebook-requests/${encodeURIComponent(row.id)}/cancel`,
      profileAuthority(context),
      { method: "POST", body: JSON.stringify({ expected_revision: row.revision }) },
      "none",
    ),
  adminList: (context: AdminRequestContext | null, offset = 0) =>
    adminV2Api<{ requests: BookRequest[] }>(
      `/organization/ebook-requests?limit=100&offset=${offset}`,
      {},
      "safe",
      context,
    ),
  adminAction: (
    row: BookRequest,
    action: EbookRequestAction,
    reason: string,
    context: AdminRequestContext | null,
  ) =>
    adminV2Api<BookRequest>(
      `/organization/ebook-requests/${encodeURIComponent(row.id)}/${action}`,
      { method: "POST", body: JSON.stringify({ expected_revision: row.revision, reason }) },
      "none",
      context,
    ),
  integrations: (context: AdminRequestContext | null) =>
    adminV2Api<EbookRequestIntegrations>(
      "/organization/ebook-request-integrations",
      {},
      "safe",
      context,
    ),
  setPolicy: (enabled: boolean, context: AdminRequestContext | null) =>
    adminV2Api<{ enabled: boolean }>(
      "/organization/ebook-request-policy",
      { method: "PUT", body: JSON.stringify({ enabled }) },
      "none",
      context,
    ),
  createIntegration: (input: EbookRequestIntegrationInput, context: AdminRequestContext | null) =>
    adminV2Api<EbookRequestIntegrations["integrations"][number]>(
      "/organization/ebook-request-integrations",
      { method: "POST", body: JSON.stringify(input) },
      "none",
      context,
    ),
  setLibrary: (id: string, libraryId: number, context: AdminRequestContext | null) =>
    adminV2Api<void>(
      `/organization/ebook-request-integrations/${encodeURIComponent(id)}/library`,
      { method: "PUT", body: JSON.stringify({ library_id: libraryId }) },
      "none",
      context,
    ),
};
