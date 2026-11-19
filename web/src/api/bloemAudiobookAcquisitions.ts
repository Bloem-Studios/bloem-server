import { adminV2Api, type AdminRequestContext } from "./adminV2Client";
import type { BookRequest, RequestIntegrationTestResult } from "./bloemBookRequests";

export interface AudiobookIntegration {
  id: string;
  name: string;
  installation_id: number;
  capability_id: string;
  library_id: number;
  library_name?: string;
  download_roots: string[];
  binding_revision: number;
  ready: boolean;
  /**
   * Saved settings, filled by the setup listing for a platform administrator
   * only. Absent there means the saved settings could not be read.
   */
  plugin_config?: AudiobookConnectionInput["plugin_config"];
  /** Names of the saved credentials; their values are never returned. */
  stored_credentials?: string[];
}
export interface AudiobookSetup {
  available: boolean;
  integrations: AudiobookIntegration[];
  libraries: { id: number; name: string; language: string; paths: string[] }[];
  installations: { id: number; name: string; capability_id: string }[];
}
export interface AudiobookConnectionInput {
  name: string;
  installation_id: number;
  capability_id: string;
  plugin_config: Record<string, string | number | boolean | string[]>;
  credentials: Record<string, string>;
}
/**
 * Edits a saved connection. plugin_config is merged over the saved settings:
 * a submitted key replaces its value, null removes it, an omitted key keeps it.
 * An empty credential keeps its saved value only while its URL is unchanged.
 */
export interface AudiobookConnectionUpdate {
  name?: string;
  plugin_config?: Record<string, string | number | boolean | string[] | null>;
  credentials?: Record<string, string>;
}
export interface AcquisitionCandidate {
  ref: string;
  source: string;
  title: string;
  size_mb: number;
  format?: string;
  h: number;
}
export interface AcquisitionDecision {
  v: number;
  phase: string;
  outcome: string;
  detail?: string;
  chosen?: AcquisitionCandidate;
  candidates?: AcquisitionCandidate[];
  heuristic?: { h: number; reason: string };
  ai?: {
    used: boolean;
    model: string;
    choice: string;
    confidence: number;
    reason: string;
    flags: string[];
    error: string;
  };
  issues?: string[];
  queries?: string[];
  sources?: { name: string; status: string }[];
  ai_calls: number;
  admin_approved: boolean;
}
export interface AcquisitionDecisionRecord {
  id: number;
  attempt: number;
  phase: string;
  outcome: string;
  created_at: string;
  decision: AcquisitionDecision;
}
export interface AudiobookAcquisition {
  request: BookRequest;
  dispatched: boolean;
  attempt: number;
  attempt_started_at?: string;
  provider_status: string;
  external_status: string;
  review_kind?: string;
  search_passes: number;
  failure_count: number;
  ai_calls: number;
  next_attempt_at?: string;
  provider_completed_at?: string;
  approved_candidate?: string;
  stuck: boolean;
  stuck_reason?: string;
  marker?: string;
  integration_id?: string;
  integration_name?: string;
  library_id?: number;
  download_roots?: string[];
  decisions?: AcquisitionDecisionRecord[];
}
export type AcquisitionFilter = "all" | "active" | "review" | "failed" | "stuck";
const route = (id: string) => `/organization/book-requests/${encodeURIComponent(id)}`;
const write = <T>(
  path: string,
  body: unknown,
  context: AdminRequestContext | null,
  method = "POST",
) => adminV2Api<T>(path, { method, body: JSON.stringify(body) }, "none", context);
export const audiobookAcquisitionsApi = {
  setup: (context: AdminRequestContext | null, signal?: AbortSignal) =>
    adminV2Api<AudiobookSetup>(
      "/organization/audiobook-request-integrations",
      { signal },
      "safe",
      context,
    ),
  createIntegration: (input: AudiobookConnectionInput, context: AdminRequestContext | null) =>
    write<AudiobookIntegration>("/organization/audiobook-request-integrations", input, context),
  updateIntegration: (
    id: string,
    input: AudiobookConnectionUpdate,
    context: AdminRequestContext | null,
  ) =>
    write<AudiobookIntegration>(
      `/organization/audiobook-request-integrations/${encodeURIComponent(id)}`,
      input,
      context,
      "PUT",
    ),
  testIntegration: (id: string, context: AdminRequestContext | null) =>
    adminV2Api<RequestIntegrationTestResult>(
      `/organization/audiobook-request-integrations/${encodeURIComponent(id)}/test`,
      { method: "POST" },
      "none",
      context,
    ),
  setLibrary: (
    id: string,
    libraryId: number,
    roots: string[],
    context: AdminRequestContext | null,
  ) =>
    write<AudiobookIntegration>(
      `/organization/audiobook-request-integrations/${encodeURIComponent(id)}/library`,
      { library_id: libraryId, download_roots: roots },
      context,
      "PUT",
    ),
  list: (
    context: AdminRequestContext | null,
    state: AcquisitionFilter = "all",
    cursor = "",
    signal?: AbortSignal,
  ) =>
    adminV2Api<{ acquisitions: AudiobookAcquisition[]; next_cursor?: string }>(
      `/organization/book-requests/acquisitions?${new URLSearchParams({ state, limit: "100", cursor })}`,
      { signal },
      "safe",
      context,
    ),
  detail: (id: string, context: AdminRequestContext | null, signal?: AbortSignal) =>
    adminV2Api<AudiobookAcquisition>(`${route(id)}/acquisition`, { signal }, "safe", context),
  approveCandidate: (
    id: string,
    revision: number,
    ref: string,
    context: AdminRequestContext | null,
  ) =>
    write<BookRequest>(
      `${route(id)}/approve-candidate`,
      { expected_revision: revision, candidate_ref: ref },
      context,
    ),
  acceptDownload: (id: string, revision: number, context: AdminRequestContext | null) =>
    write<BookRequest>(`${route(id)}/accept-download`, { expected_revision: revision }, context),
  retry: (id: string, revision: number, context: AdminRequestContext | null) =>
    write<BookRequest>(`${route(id)}/retry`, { expected_revision: revision }, context),
};
