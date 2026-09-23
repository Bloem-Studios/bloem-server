// Bloem-owned API types. Kept out of the Silo-owned types.ts so upstream merges stay clean.

export type AdminContextKey = "platform" | `organization:${string}`;

export interface AdminContextSummary {
  key: AdminContextKey;
  scope: "platform" | "organization";
  organizationId?: string;
  name: string;
  status: "active" | "suspended";
  authority: "platform_admin" | "organization_admin";
  policyRevision: number;
  securityRevision: number;
}

export interface AdminContextFailure {
  code: string;
  message: string;
}

export interface AdminContextValue {
  available: AdminContextSummary[];
  active: AdminContextSummary | null;
  switching: boolean;
  failure: AdminContextFailure | null;
  switchContext(key: AdminContextKey, beforeNavigate?: () => void): Promise<void>;
  clearContext(reason?: AdminContextFailure): void;
}

export interface AdminContextSessionResponse {
  accessToken: string;
  expiresAt: string;
  context: AdminContextSummary;
}

export interface EntitlementTemplatePolicy {
  /** null selects every enabled library. */
  library_ids: number[] | null;
  playback_allowed: boolean;
  max_streams: number;
  max_profiles: number;
  transcode_allowed: boolean;
  max_transcodes: number;
  download_allowed: boolean;
  download_transcode_allowed: boolean;
  max_playback_quality: string;
  requests_allowed: boolean;
  /** null permits every access-group permission. */
  allowed_permissions: string[] | null;
}

export interface EntitlementTemplate {
  key: string;
  name: string;
  revision: number;
  enabled: boolean;
  archived: boolean;
  created_at?: string;
  description?: string;
  policy: EntitlementTemplatePolicy;
}

export interface EntitlementTemplateInput {
  key: string;
  name: string;
  enabled: boolean;
  policy: EntitlementTemplatePolicy;
}

// Bloem extends Silo-owned interfaces through module augmentation instead of
// editing types.ts.
declare module "./types" {
  interface AppNotification {
    title?: string;
    body?: string;
    severity?: "info" | "warning" | "critical";
    deeplink?: string;
    image_url?: string;
    dismissible?: boolean;
    cta?: { label: string; url: string };
    expires_at?: string;
    dismissed_at?: string;
  }

  interface ApiError {
    fields?: Record<string, string>;
  }
}

export interface PolicyVendorModule {
  path: string;
  source: string;
}

export interface PolicyCompileIssue {
  row: number;
  col: number;
  message: string;
}

export interface PolicyVersionSummary {
  id: number;
  document_id: number;
  version_number: number;
  source_sha256: string;
  compiled_ok: boolean;
  compile_error?: string;
  created_by_user_id?: number;
  comment?: string;
  created_at: string;
}

export interface PolicyVersion extends PolicyVersionSummary {
  source?: string;
}

export interface PolicyDocument {
  id: number;
  domain: string;
  name: string;
  enabled: boolean;
  active_version_id?: number;
  active_version?: PolicyVersion;
  created_at: string;
  updated_at: string;
}

export interface PolicyCreateVersionResult {
  id: number;
  version_number: number;
  compiled_ok: boolean;
}

export interface PolicyActivateVersionResult {
  active_version_id: number;
  generation: number;
}

export interface PolicySetDocumentEnabledResult {
  id: number;
  enabled: boolean;
  generation: number;
}

export interface PolicyValidateResult {
  compiled_ok: boolean;
  errors: PolicyCompileIssue[];
}

export interface LiveTVTuner {
  id: string;
  type: string;
  device_id: string;
  discover_url: string;
  base_url: string;
  model: string;
  firmware: string;
  tuner_count: number;
  status: string;
  channel_count: number;
  last_error: string;
  last_scan_at?: string;
  transcode_codecs?: string[];
}

export interface LiveTVTunersResponse {
  tuners: LiveTVTuner[];
}
export interface LiveTVDiscoveredTuner {
  kind: "hdhomerun" | "dispatcharr";
  device_id: string;
  friendly_name: string;
  model: string;
  firmware: string;
  tuner_count: number;
  discover_url: string;
  base_url: string;
  source: "udp" | "probe";
  already_added: boolean;
}
export interface LiveTVDiscoverTunersResponse {
  candidates: LiveTVDiscoveredTuner[];
  notes?: string[];
}
export interface LiveTVChannel {
  id: string;
  tuner_id: string;
  number: string;
  number_override?: string | null;
  callsign: string;
  name: string;
  logo_url: string;
  hd: boolean;
  enabled: boolean;
  stream_url: string;
  guide_station_id: string;
}
export interface LiveTVChannelsResponse {
  channels: LiveTVChannel[];
}
export interface LiveTVGuideSource {
  id: string;
  type: "schedules_direct" | "xml_sync" | "xtream";
  priority: number;
  enabled: boolean;
  display_name: string;
  config: Record<string, string>;
  status: string;
  last_error: string;
  last_sync_at?: string;
  next_sync_at?: string;
}
export interface LiveTVGuideSourcesResponse {
  guide_sources: LiveTVGuideSource[];
}
export interface SchedulesDirectLineupOption {
  lineup: string;
  name: string;
  transport: string;
  location: string;
  headend: string;
}
export interface SchedulesDirectLineupsResponse {
  lineups: SchedulesDirectLineupOption[];
}
export interface XMLSyncLineupOption {
  lineup: string;
  name: string;
  transport: string;
  location: string;
  headend: string;
  device: string;
}
export interface XMLSyncLineupsResponse {
  lineups: XMLSyncLineupOption[];
}
export interface LiveTVProgram {
  id: string;
  channel_id: string;
  source_id?: string;
  series_id: string;
  external_id?: string;
  start: string;
  stop: string;
  title: string;
  subtitle: string;
  description: string;
  season?: number | null;
  episode?: number | null;
  genres: string[];
  image_url: string;
  is_new: boolean;
  is_live: boolean;
}
export interface LiveTVGuideResponse {
  programs: LiveTVProgram[];
  start: string;
  end: string;
}
export interface LiveTVSessionStartResponse {
  session_id: string;
  playback_ticket: string;
  hls_url: string;
  stream_url?: string;
  transport?: "mpegts" | "hls";
  note?: string;
}
export interface LiveTVRecording {
  id: string;
  program_id?: string;
  channel_id: string;
  series_rule_id?: string;
  status: string;
  path?: string;
  library_item_id?: string;
  start: string;
  stop: string;
  title: string;
  last_error?: string;
}
export interface LiveTVRecordingsResponse {
  recordings: LiveTVRecording[];
}
export interface LiveTVSeriesRule {
  id: string;
  series_id: string;
  channel_id?: string | null;
  title_match: string;
  new_only: boolean;
  keep_last: number;
  enabled: boolean;
}
export interface LiveTVSeriesRulesResponse {
  series_rules: LiveTVSeriesRule[];
}

export interface PolicySimulateRequest {
  domain: string;
  source?: string;
  input: unknown;
}

export interface PolicySimulateResult {
  decision: unknown;
  eval_time_ns: number;
  generation: number;
}

export interface PolicyDecisionEntry {
  id: number;
  timestamp: string;
  decision_name: string;
  policy_generation: number;
  user_id?: number;
  profile_id?: string;
  session_id?: string;
  request_id?: string;
  node_id?: string;
  allowed: boolean | null;
  eval_time_ns: number;
  input_digest: string;
  input_sample?: unknown;
  result_sample?: unknown;
  error?: string;
}

export interface PolicyDecisionListResult {
  entries: PolicyDecisionEntry[];
  next_cursor?: string;
}
