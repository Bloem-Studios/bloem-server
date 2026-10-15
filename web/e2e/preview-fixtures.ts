import { readFileSync } from "node:fs";
import path from "node:path";
const root = path.resolve(import.meta.dirname, "../../contracts/api/v2/fixtures");
const fixture = (name: string) => JSON.parse(readFileSync(path.join(root, `${name}.json`), "utf8"));
export interface PreviewFixtureState {
  expired?: boolean;
  invalidTicket?: boolean;
  kind?: "guest" | "personal";
  exchanges: number;
  unexpected: string[];
}
export function previewFixture(
  method: string,
  pathname: string,
  state: PreviewFixtureState,
): { status: number; body?: unknown } {
  const base = "https://store.example.test";
  const presentation = {
    kind: state.kind ?? "guest",
    ends_at: new Date(Date.now() + 30 * 60000).toISOString(),
    cta_url: `${base}/trial?plan=7`,
    cta_label: "Subscribe to Acme",
    launch_url: `${base}/preview/launch`,
    storefront_url: base,
  };
  const problem = {
    type: "https://siloserver.org/docs/api/v2/problems/session_expired",
    title: "Session ended",
    status: 401,
  };
  if (pathname === "/api/bloem/v1/preview/status")
    return {
      status: 200,
      body:
        state.kind === "personal"
          ? { enabled: true }
          : { enabled: true, brand: "Acme", ...presentation },
    };
  if (pathname === "/api/bloem/v1/preview/sessions") {
    state.exchanges++;
    return state.invalidTicket
      ? { status: 403, body: { code: "preview_ticket_invalid" } }
      : {
          status: 201,
          body: {
            access_token: "synthetic-preview-access",
            refresh_token: "synthetic-preview-refresh",
            expires_in: 1800,
            expires_at: presentation.ends_at,
            item: "",
            ref: "signed-affiliate",
          },
        };
  }
  if (pathname === "/api/v2/auth/refresh")
    return state.expired
      ? { status: 401, body: problem }
      : {
          status: 200,
          body: {
            access_token: "synthetic-preview-access",
            refresh_token: "synthetic-preview-rotated",
            expires_in: 1800,
          },
        };
  if (pathname === "/api/v2/auth/logout" || pathname === "/api/bloem/v1/preview/events")
    return { status: 204 };
  if (state.expired && pathname === "/api/v2/account/me") return { status: 401, body: problem };
  if (method !== "GET" && pathname !== "/api/v2/catalog/query") {
    state.unexpected.push(`${method} ${pathname}`);
    return { status: 403, body: { code: "preview_only" } };
  }
  if (pathname === "/api/v2/account/me")
    return {
      status: 200,
      body: {
        ...fixture("get_current_user_ok"),
        permissions: [],
        download_allowed: false,
        playback_allowed: false,
        preview: presentation,
      },
    };
  if (pathname === "/api/v2/system/setup")
    return { status: 200, body: { needs_setup: false, wizard_completed: true } };
  if (pathname === "/api/v2/auth/providers") return { status: 200, body: { items: [] } };
  if (pathname === "/api/v2/theme/branding") return { status: 200, body: { server_name: "Acme" } };
  if (pathname === "/api/v2/theme/admin-css") return { status: 200, body: {} };
  if (pathname === "/api/v2/profiles") return { status: 200, body: { items: [] } };
  if (pathname === "/api/v2/user/libraries")
    return {
      status: 200,
      body: { items: [{ id: "25", name: "Series", type: "series", sort_order: 0 }] },
    };
  if (pathname === "/api/v2/settings/contract/capabilities")
    return { status: 200, body: fixture("get_settings_contract_capabilities_ok") };
  if (pathname === "/api/v2/home/capabilities")
    return { status: 200, body: fixture("get_home_capabilities_ok") };
  if (pathname === "/api/v2/home/layout")
    return {
      status: 200,
      body: {
        sections: [
          {
            id: "recently_added",
            section_type: "recently_added",
            title: "Recently added",
            item_limit: 20,
            featured: true,
            is_custom: false,
            customized: false,
          },
        ],
        included_sections: [
          {
            id: "recently_added",
            section_type: "recently_added",
            title: "Recently added",
            item_limit: 20,
            featured: true,
            total_count: 1,
            items: [
              {
                content_id: "series:severance",
                type: "series",
                title: "Severance",
                overview: "A mystery about work and memory.",
                year: 2022,
                genres: [],
                studios: [],
                poster_url: "",
                backdrop_url: "",
              },
            ],
          },
        ],
      },
    };
  if (pathname === "/api/v2/catalog/series/series:severance/seasons")
    return {
      status: 200,
      body: {
        items: [
          ...fixture("list_series_seasons_ok").items,
          {
            ...fixture("list_series_seasons_ok").items[0],
            content_id: "series:severance-S02",
            season_number: 2,
            title: "Season 2",
          },
        ],
      },
    };
  if (pathname === "/api/v2/catalog/series/series:severance/seasons/1")
    return { status: 200, body: fixture("get_series_season_ok") };
  if (pathname === "/api/v2/catalog/series/series:severance/seasons/1/episodes")
    return { status: 200, body: fixture("list_season_episodes_ok") };
  if (pathname.startsWith("/api/v2/catalog/items/") && pathname.endsWith("/episodes"))
    return { status: 200, body: fixture("list_season_episodes_ok") };
  if (pathname.startsWith("/api/v2/catalog/items/") && pathname.endsWith("/similar"))
    return { status: 200, body: { items: [] } };
  if (pathname.startsWith("/api/v2/catalog/items/")) {
    const id = pathname.slice("/api/v2/catalog/items/".length);
    if (id.includes("/")) return { status: 200, body: { items: [] } };
    const type = id.startsWith("episode:") ? "episode" : id.includes("-S01") ? "season" : "series";
    const detail = {
      ...fixture("get_catalog_item_ok"),
      content_id: id,
      type,
      title:
        type === "series" ? "Severance" : type === "season" ? "Season 1" : "Good News About Hell",
      overview: "A mystery about work and memory.",
      genres: [],
      cast: [],
      crew: [],
      versions: [],
      themes: undefined,
      work_formats: [],
      series_id: type === "series" ? undefined : "series:severance",
      series_title: "Severance",
      season_number: type === "series" ? undefined : 1,
      episode_number: type === "episode" ? 1 : undefined,
      tmdb_id: "",
      imdb_id: "",
      tvdb_id: "",
      poster_url: "",
      backdrop_url: "",
      logo_url: "",
      poster_thumbhash: "",
      backdrop_thumbhash: "",
      still_thumbhash: "",
      still_url: "",
      user_data: undefined,
      user_state: undefined,
      play_content_id: "episode:severance-s01e01",
      episode_count: 2,
      season_count: 1,
    };
    return { status: 200, body: detail };
  }
  if (pathname === "/api/v2/playback/capabilities")
    return {
      status: 200,
      body: {
        state: "disabled",
        allowed: false,
        denial: {
          code: "preview_only",
          message: "Subscribe to watch",
          cta_url: presentation.cta_url,
          cta_label: presentation.cta_label,
        },
      },
    };
  if (pathname === "/api/v2/recommendations/similar/series:severance")
    return { status: 200, body: { items: [] } };
  if (pathname === "/api/v2/settings/overlay-config") return { status: 200, body: { fields: [] } };
  if (pathname === "/api/v1/theme/branding") return { status: 200, body: { server_name: "Acme" } };
  if (pathname.endsWith("/capabilities") || pathname.endsWith("/capability"))
    return {
      status: 200,
      body: {
        state: "disabled",
        allowed: false,
        supported: false,
        available: false,
        in_app: { enabled: false },
      },
    };
  if (pathname === "/api/bloem/v1/ambience") return { status: 200, body: { enabled: false } };
  if (pathname === "/api/bloem/v1/capabilities") return { status: 200, body: {} };
  if (pathname === "/api/bloem/v1/server/identity")
    return { status: 200, body: { id: "synthetic-server" } };
  if (pathname === "/api/v2/collections/server") return { status: 200, body: { libraries: [] } };
  if (pathname === "/api/v2/calendar") return { status: 200, body: { items: [] } };
  state.unexpected.push(`${method} ${pathname}`);
  return {
    status: 403,
    body: {
      type: "https://siloserver.org/docs/api/v2/problems/permission_denied",
      title: "Unavailable in preview",
      status: 403,
      code: "preview_only",
    },
  };
}
