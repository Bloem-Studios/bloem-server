/**
 * The search mirror's status, as GET /api/bloem/v2/admin/search/mirror returns
 * it (the SearchMirrorStatus struct in internal/catalog/bloem_search_mirror_status.go).
 * Written by hand: the route is Bloem's own, and its codes are English data.
 */

/** The states the server reports; "off" means no mirror state at all. */
export type BloemSearchMirrorState =
  | "off"
  | "following"
  | "copying"
  | "waiting"
  | "unreachable"
  | "needs_full_copy"
  | "misconfigured"
  | "failing"
  | "settling";

/** Progress of a full copy into a side index. */
export interface BloemSearchMirrorCopy {
  done: number;
  total: number;
  /** done / total, 0 to 1. */
  ratio: number;
  started_at: string | null;
}

export interface BloemSearchMirrorStatus {
  configured: boolean;
  /** Where the settings came from: "environment", or "" when not configured. */
  source: string;
  /** The mirror's address with any password removed. */
  url: string;
  /** Where the API key comes from: "file", "environment", "none", or "". */
  key_source: string;
  state: BloemSearchMirrorState;
  /** An English code saying why, such as "past_cap"; absent when there is none. */
  reason?: string;
  settling: boolean;
  settling_overdue: boolean;
  lag_seconds: number | null;
  /** RFC 3339 time. */
  up_to_date_as_of: string | null;
  pending_events: number | null;
  pending_events_capped: boolean;
  mirror_index: string;
  main_index: string;
  copy: BloemSearchMirrorCopy | null;
  last_full_copy_at: string | null;
  last_error?: string;
  error_since: string | null;
  dead_letters: number;
  max_lag_seconds: number;
  role_swaps: number;
  /** True when this database holds mirror state, whether or not this server is configured. */
  state_exists: boolean;
  /**
   * Warning codes: "configured_on_one_copy_only", "settling_overdue" and
   * "stood_down" today; a newer server may send more.
   */
  warnings: string[];
}
