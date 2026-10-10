import { nativeApiWithProfileRequestContext } from "./bloemClient";
import { captureProfileRequestContext, type ProfileRequestContextSnapshot } from "./client";
import { adminV2Api, type AdminRequestContext } from "./adminV2Client";

export interface BookRequest {
  id: string;
  organization_id: string;
  media_type: "audiobook";
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
  fulfilment: "awaiting_fulfilment" | "library" | "manual";
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
}
export type BookRequestAction = "approve" | "decline" | "fulfil" | "cancel";
export function bookRequestState(row: BookRequest): string {
  if (row.outcome === "declined") return "Declined";
  if (row.outcome === "cancelled") return "Cancelled";
  if (row.status === "pending") return "Awaiting approval";
  if (row.status === "approved") return "Approved · awaiting fulfilment";
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
