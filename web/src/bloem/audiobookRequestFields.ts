import { bloemT } from "@/bloem/i18n";

// Structured controls from bloem.requests.audiobooks v0.1.0 connection schema.
// Keep this owned projection aligned with the plugin manifest; never send diagnostic keys.
// Labels and help come from Bloem's catalog, formatted when they are shown.
export type AudiobookConfigGroup = "downloadClients" | "sources" | "matching" | "ai";
export interface AudiobookConfigField {
  key: string;
  label: () => string;
  description?: () => string;
  group: AudiobookConfigGroup;
  type: string;
  default: string | number | boolean | string[];
  minimum?: number;
  maximum?: number;
  maxLength?: number;
  maxItems?: number;
  enum?: string[];
  items?: { type: string; maxLength: number };
}
export const audiobookConfigFields: AudiobookConfigField[] = [
  {
    key: "audiobookbay_enabled",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayEnabled.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayEnabled.description"),
    group: "sources",
    default: true,
    type: "boolean",
  },
  {
    key: "audiobookbay_base_url",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayBaseUrl.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayBaseUrl.description"),
    group: "sources",
    default: "https://audiobookbay.lu",
    maxLength: 4096,
    type: "string",
  },
  {
    key: "audiobookbay_search_pages",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbaySearchPages.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbaySearchPages.description"),
    group: "sources",
    default: 2,
    maximum: 5,
    minimum: 1,
    type: "integer",
  },
  {
    key: "audiobookbay_resolve_limit",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayResolveLimit.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayResolveLimit.description"),
    group: "sources",
    default: 5,
    maximum: 8,
    minimum: 1,
    type: "integer",
  },
  {
    key: "audiobookbay_trackers",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayTrackers.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.audiobookbayTrackers.description"),
    group: "sources",
    default: [
      "udp://tracker.openbittorrent.com:80",
      "udp://opentor.org:2710",
      "udp://tracker.ccc.de:80",
      "udp://tracker.blackunicorn.xyz:6969",
    ],
    items: {
      maxLength: 300,
      type: "string",
    },
    maxItems: 20,
    type: "array",
  },
  {
    key: "abook_enabled",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.abookEnabled.label"),
    description: () => bloemT("bloem.admin.bookRequests.audiobookConfig.abookEnabled.description"),
    group: "sources",
    default: false,
    type: "boolean",
  },
  {
    key: "abook_base_url",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.abookBaseUrl.label"),
    description: () => bloemT("bloem.admin.bookRequests.audiobookConfig.abookBaseUrl.description"),
    group: "sources",
    default: "https://abook.link/book",
    maxLength: 4096,
    type: "string",
  },
  {
    key: "abook_auto_thank",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.abookAutoThank.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.abookAutoThank.description"),
    group: "sources",
    default: true,
    type: "boolean",
  },
  {
    key: "nzbking_enabled",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.nzbkingEnabled.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.nzbkingEnabled.description"),
    group: "sources",
    default: true,
    type: "boolean",
  },
  {
    key: "nzbking_base_url",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.nzbkingBaseUrl.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.nzbkingBaseUrl.description"),
    group: "sources",
    default: "https://nzbking.com",
    maxLength: 4096,
    type: "string",
  },
  {
    key: "nzbking_standalone",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.nzbkingStandalone.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.nzbkingStandalone.description"),
    group: "sources",
    default: true,
    type: "boolean",
  },
  {
    key: "qbittorrent_url",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentUrl.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentUrl.description"),
    group: "downloadClients",
    default: "",
    maxLength: 4096,
    type: "string",
  },
  {
    key: "qbittorrent_username",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentUsername.label"),
    group: "downloadClients",
    default: "",
    maxLength: 256,
    type: "string",
  },
  {
    key: "qbittorrent_category",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentCategory.label"),
    group: "downloadClients",
    default: "bloem-audiobooks",
    maxLength: 64,
    type: "string",
  },
  {
    key: "qbittorrent_save_root",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentSaveRoot.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentSaveRoot.description"),
    group: "downloadClients",
    default: "",
    maxLength: 1024,
    type: "string",
  },
  {
    key: "qbittorrent_stall_hours",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentStallHours.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.qbittorrentStallHours.description"),
    group: "downloadClients",
    default: 24,
    maximum: 720,
    minimum: 1,
    type: "integer",
  },
  {
    key: "nzbget_url",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.nzbgetUrl.label"),
    description: () => bloemT("bloem.admin.bookRequests.audiobookConfig.nzbgetUrl.description"),
    group: "downloadClients",
    default: "",
    maxLength: 4096,
    type: "string",
  },
  {
    key: "nzbget_username",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.nzbgetUsername.label"),
    group: "downloadClients",
    default: "",
    maxLength: 256,
    type: "string",
  },
  {
    key: "nzbget_category",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.nzbgetCategory.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.nzbgetCategory.description"),
    group: "downloadClients",
    default: "bloem-audiobooks",
    maxLength: 64,
    type: "string",
  },
  {
    key: "allow_http_download_clients",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.allowHttpDownloadClients.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.allowHttpDownloadClients.description"),
    group: "downloadClients",
    default: false,
    type: "boolean",
  },
  {
    key: "allow_private_source_hosts",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.allowPrivateSourceHosts.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.allowPrivateSourceHosts.description"),
    group: "downloadClients",
    default: false,
    type: "boolean",
  },
  {
    key: "allow_loopback_targets",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.allowLoopbackTargets.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.allowLoopbackTargets.description"),
    group: "downloadClients",
    default: false,
    type: "boolean",
  },
  {
    key: "min_title_coverage",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.minTitleCoverage.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.minTitleCoverage.description"),
    group: "matching",
    default: 0.6,
    maximum: 1,
    minimum: 0.3,
    type: "number",
  },
  {
    key: "min_author_coverage",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.minAuthorCoverage.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.minAuthorCoverage.description"),
    group: "matching",
    default: 0.5,
    maximum: 1,
    minimum: 0,
    type: "number",
  },
  {
    key: "max_candidates",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.maxCandidates.label"),
    description: () => bloemT("bloem.admin.bookRequests.audiobookConfig.maxCandidates.description"),
    group: "matching",
    default: 10,
    maximum: 10,
    minimum: 3,
    type: "integer",
  },
  {
    key: "verify_min_total_mb",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.verifyMinTotalMb.label"),
    group: "matching",
    default: 20,
    maximum: 10000,
    minimum: 0,
    type: "integer",
  },
  {
    key: "verify_min_duration_minutes",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.verifyMinDurationMinutes.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.verifyMinDurationMinutes.description"),
    group: "matching",
    default: 30,
    maximum: 6000,
    minimum: 0,
    type: "integer",
  },
  {
    key: "verify_local_client_prefix",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.verifyLocalClientPrefix.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.verifyLocalClientPrefix.description"),
    group: "matching",
    default: "",
    maxLength: 1024,
    type: "string",
  },
  {
    key: "verify_local_prefix",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.verifyLocalPrefix.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.verifyLocalPrefix.description"),
    group: "matching",
    default: "",
    maxLength: 1024,
    type: "string",
  },
  {
    key: "ai_enabled",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiEnabled.label"),
    description: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiEnabled.description"),
    group: "ai",
    default: false,
    type: "boolean",
  },
  {
    key: "ai_base_url",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiBaseUrl.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.aiBaseUrl.description", {
        example: "https://generativelanguage.googleapis.com/v1beta/openai",
      }),
    group: "ai",
    default: "",
    maxLength: 4096,
    type: "string",
  },
  {
    key: "ai_chat_model",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiChatModel.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.aiChatModel.description", {
        example: "gemini-flash-latest",
      }),
    group: "ai",
    default: "",
    maxLength: 128,
    type: "string",
  },
  {
    key: "ai_allow_http",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiAllowHttp.label"),
    description: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiAllowHttp.description"),
    group: "ai",
    default: false,
    type: "boolean",
  },
  {
    key: "ai_response_format",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiResponseFormat.label"),
    group: "ai",
    default: "json_object",
    enum: ["json_object", "json_schema"],
    maxLength: 32,
    type: "string",
  },
  {
    key: "ai_reasoning_effort",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiReasoningEffort.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.aiReasoningEffort.description"),
    group: "ai",
    default: "",
    enum: ["", "none", "low", "medium"],
    maxLength: 32,
    type: "string",
  },
  {
    key: "ai_parameter_profile",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiParameterProfile.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.aiParameterProfile.description"),
    group: "ai",
    default: "standard",
    enum: ["standard", "reasoning"],
    maxLength: 32,
    type: "string",
  },
  {
    key: "ai_min_confidence",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiMinConfidence.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.aiMinConfidence.description"),
    group: "ai",
    default: 0.75,
    maximum: 0.99,
    minimum: 0.5,
    type: "number",
  },
  {
    key: "ai_max_calls_per_request",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiMaxCallsPerRequest.label"),
    group: "ai",
    default: 6,
    maximum: 20,
    minimum: 1,
    type: "integer",
  },
  {
    key: "ai_timeout_seconds",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiTimeoutSeconds.label"),
    group: "ai",
    default: 20,
    maximum: 60,
    minimum: 5,
    type: "integer",
  },
  {
    key: "ai_max_tokens",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiMaxTokens.label"),
    group: "ai",
    default: 1024,
    maximum: 4096,
    minimum: 256,
    type: "integer",
  },
  {
    key: "ai_query_expansion",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiQueryExpansion.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.aiQueryExpansion.description"),
    group: "ai",
    default: true,
    type: "boolean",
  },
  {
    key: "ai_max_queries",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiMaxQueries.label"),
    group: "ai",
    default: 3,
    maximum: 5,
    minimum: 1,
    type: "integer",
  },
  {
    key: "ai_verify_downloads",
    label: () => bloemT("bloem.admin.bookRequests.audiobookConfig.aiVerifyDownloads.label"),
    description: () =>
      bloemT("bloem.admin.bookRequests.audiobookConfig.aiVerifyDownloads.description"),
    group: "ai",
    default: true,
    type: "boolean",
  },
];
export function audiobookConfigDefaults() {
  return Object.fromEntries(audiobookConfigFields.map((field) => [field.key, field.default]));
}
