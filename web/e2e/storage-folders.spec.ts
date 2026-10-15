import { expect, test, type Route } from "@playwright/test";
import profiles from "../../contracts/api/v2/fixtures/list_profiles_ok.json" with { type: "json" };
import settingsCapabilities from "../../contracts/api/v2/fixtures/get_settings_contract_capabilities_ok.json" with { type: "json" };
import effectiveSettings from "../../contracts/api/v2/fixtures/list_effective_settings_ok.json" with { type: "json" };
import eventsTicket from "../../contracts/api/v2/fixtures/events_socket_ticket.json" with { type: "json" };

const directory = (name: string) => ({
  name,
  logical_path: name,
  selection_ref: `opaque-${name}`,
});

for (const width of [390, 1440]) {
  for (const kind of ["s3", "bookwarehouse"] as const) {
    test(`${width}px ${kind} browses and saves multiple folders in one library`, async ({
      page,
    }, info) => {
      await page.setViewportSize({ width, height: 900 });
      const saved: Array<{ library: { name: string }; selections: unknown[] }> = [];
      const localPaths = ["/local/books", "/local/archive", "/local/new"];
      const unexpected: string[] = [];
      const source = {
        source_key: "10000000-0000-0000-0000-000000000001",
        plugin_id: `bloem.storage.${kind}`,
        provider_source_id: "Archive",
        root_entry_id: "root",
        configuration_revision: 1,
        enabled: true,
        configured: true,
        owner_kind: "platform",
      };
      const root = directory("Root");
      const archive = directory(kind === "bookwarehouse" ? "EPUB" : "Archive");
      const first = directory("70s");
      const second = directory("80s");
      const third = directory("90s");
      const folderPage = (ref: string | null) => {
        if (!ref || ref === root.selection_ref)
          return { current: root, breadcrumbs: [], directories: [archive], next_cursor: "" };
        if (ref === archive.selection_ref)
          return {
            current: archive,
            breadcrumbs: [root],
            directories: [first, second, third],
            next_cursor: "",
          };
        const current = [first, second, third].find((dir) => dir.selection_ref === ref);
        if (!current) throw new Error("Unexpected directory reference");
        return { current, breadcrumbs: [root, archive], directories: [], next_cursor: "" };
      };
      await page.addInitScript((profile) => {
        localStorage.setItem("refresh_token", "fixture-refresh");
        localStorage.setItem("profile_id", "p-owner");
        localStorage.setItem("current_profile", JSON.stringify(profile));
        class InertWebSocket extends EventTarget {
          static readonly CONNECTING = 0;
          static readonly OPEN = 1;
          static readonly CLOSING = 2;
          static readonly CLOSED = 3;
          readonly readyState = 0;
          onopen = null;
          onmessage = null;
          onclose = null;
          onerror = null;
          send() {}
          close() {}
        }
        Object.defineProperty(window, "WebSocket", { value: InertWebSocket });
      }, profiles.items[0]);
      await page.route("**/api/**", async (route) => {
        const req = route.request();
        const url = new URL(req.url());
        const path = url.pathname;
        if (!path.startsWith("/api/")) return route.continue();
        const method = req.method();
        const json = (body: unknown, status = 200) => reply(route, body, status);
        if (method === "GET" && path === "/api/v2/system/setup")
          return json({ needs_setup: false, wizard_completed: true });
        if (method === "GET" && path === "/api/v2/auth/providers") return json({ items: [] });
        if (method === "POST" && path === "/api/v2/events/ws-ticket") return json(eventsTicket);
        if (method === "POST" && path === "/api/v2/auth/refresh")
          return json({
            access_token: "fixture-access",
            refresh_token: "fixture-next",
            expires_in: 3600,
          });
        if (method === "GET" && path === "/api/v2/account/me")
          return json({ id: "7", username: "fixture-admin", role: "admin", permissions: [] });
        if (method === "GET" && path === "/api/bloem/v1/organizations")
          return json({ organizations: [] });
        if (method === "POST" && path === "/api/bloem/v1/admin/session")
          return json({
            access_token: "fixture-platform",
            expires_at: new Date(Date.now() + 3600_000).toISOString(),
            context: {
              key: "platform",
              scope: "platform",
              name: "Platform",
              status: "active",
              authority: "platform_admin",
            },
          });
        if (method === "GET" && path.endsWith("/native-storage/capabilities"))
          return json({
            library_location_browsing: true,
            library_location_atomic_editing: true,
            library_location_mixing: true,
            supported_source_media: [{ source_kind: kind, media_kinds: ["ebooks"] }],
          });
        if (method === "GET" && path.endsWith("/native-storage/sources"))
          return json({ sources: [source], next_after: null });
        if (method === "GET" && path.endsWith(`/sources/${source.source_key}/browse`))
          return json(folderPage(url.searchParams.get("directory_ref")));
        if (method === "POST" && path === "/api/bloem/v1/admin/platform/library-locations") {
          const body = req.postDataJSON();
          saved.push(body);
          return json(
            {
              library: { ...body.library, id: 7 },
              locations: {
                revision: 1,
                edit_token: "fixture-edit",
                paths: body.library.paths,
                selections: body.selections,
                locations: [],
              },
            },
            201,
          );
        }
        if (method === "GET" && path === "/api/v2/libraries") return json({ items: [] });
        if (method === "GET" && path === "/api/v2/libraries/capabilities") return json({});
        if (method === "GET" && path === "/api/v2/libraries/provider-defaults")
          return json({ levels: [] });
        if (method === "GET" && path === "/api/v2/settings/contract/capabilities")
          return json(settingsCapabilities);
        if (method === "GET" && path === "/api/v2/settings/values/effective")
          return json(effectiveSettings);
        if (method === "GET" && path === "/api/v2/profiles") return json(profiles);
        if (method === "GET" && path === "/api/v2/user/libraries") return json({ items: [] });
        if (method === "GET" && path === "/api/v2/home/layout") return json({ sections: [] });
        if (method === "GET" && path === "/api/v2/onboarding/state")
          return json({ tour_id: "welcome", done: true });
        if (method === "GET" && path === "/api/v2/theme/admin-css")
          return json({ vars: "{}", raw_css: "" });
        if (method === "GET" && ["/api/v1/theme/branding", "/api/v2/theme/branding"].includes(path))
          return json({});
        if (method === "GET" && path === "/api/v2/policy/capability")
          return json({ available: false });
        if (method === "GET" && path === "/api/v2/admin/system/build")
          return json({ display: "fixture", available: true });
        if (method === "GET" && path === "/api/v2/admin/trickplay/libraries")
          return json({ items: [] });
        if (method === "GET" && path === "/api/v2/admin/downloads/preparations")
          return json({ items: [] });
        if (method === "GET" && path === "/api/v2/libraries/realtime-monitoring")
          return json({ enabled: false, libraries: [] });
        if (method === "GET" && path === "/api/v2/admin/requests/counts")
          return json({ pending: 0, approved: 0, denied: 0 });
        if (method === "GET" && path === "/api/v2/admin/server/status") return json({});
        if (method === "GET" && path === "/api/v2/admin/settings/effective") return json({values:{}});
        if (method === "GET" && path === "/api/v2/admin/settings/sensitive-status") return json({configured:[]});
        if (method === "GET" && path === "/api/v2/admin/filesystem/browse")
          return json({
            path: "/",
            parent: null,
            items: localPaths.map((path) => ({ path, name: path.slice(1) })),
            page: { has_more: false },
          });
        if (
          method === "GET" &&
          [
            "/api/v2/admin/sessions",
            "/api/v2/admin/plugins/installations",
            "/api/v2/admin/tasks",
            "/api/v2/admin/jobs",
            "/api/v2/libraries/unmatched-items",
            "/api/v2/libraries/metadata-match-queue",
          ].includes(path)
        )
          return json({ items: [], page: { has_more: false } });
        if (
          method === "GET" &&
          [
            "/api/v1/libraries",
            "/api/v1/admin/tasks",
            "/api/v1/admin/sessions",
            "/api/v1/admin/plugins/installations",
          ].includes(path)
        )
          return json([]);
        unexpected.push(`${method} ${path}`);
        return json({ error: "fixture_request_unhandled" }, 404);
      });
      await page.goto("/admin/libraries");
      await page.getByRole("button", { name: "Add Library", exact: true }).click();
      const form = page.getByRole("dialog", { name: "Add Library", exact: true });
      await form.getByLabel("Name", { exact: true }).fill("Archive library");
      await form.getByRole("radio", { name: "Ebooks", exact: true }).click();
      // The ordinary editor's mobile rail hides tab labels; Folders is second.
      await form.getByRole("tab").nth(1).click();
      await form.getByRole("button", { name: "Browse folders", exact: true }).click();
      const localBrowser = page.getByRole("dialog", {
        name: "Browse Library Folders",
        exact: true,
      });
      await expect(localBrowser.getByRole("button", { name: "Select", exact: true })).toHaveCount(
        3,
      );
      for (let i = 0; i < localPaths.length; i++)
        await localBrowser.getByRole("button", { name: "Select", exact: true }).first().click();
      await localBrowser.getByRole("button", { name: "Add 3 Folders", exact: true }).click();
      await form.getByRole("combobox", { name: "Choose a storage source" }).click();
      await page
        .getByRole("option")
        .filter({ hasText: kind === "bookwarehouse" ? "Bookwarehouse" : "Archive" })
        .click();
      await form.getByRole("button", { name: "Browse source folders" }).click();
      const browser = page
        .getByRole("dialog")
        .filter({ has: page.getByText("Choose folders for this library.", { exact: true }) });
      await browser.getByRole("button", { name: archive.name, exact: true }).click();
      await expect(browser.getByRole("button", { name: "Up", exact: true })).toBeEnabled();
      await browser.getByRole("button", { name: first.name, exact: true }).click();
      await expect(browser.locator('[aria-current="page"]')).toHaveText(first.name);
      await browser.getByRole("button", { name: "Up", exact: true }).click();
      await browser.getByRole("checkbox", { name: `Select ${first.name}`, exact: true }).check();
      await browser.getByRole("button", { name: second.name, exact: true }).click();
      await browser.getByRole("button", { name: "Up", exact: true }).click();
      await browser.getByRole("checkbox", { name: `Select ${second.name}`, exact: true }).check();
      await browser.getByRole("checkbox", { name: `Select ${third.name}`, exact: true }).check();
      await expect(browser.getByText("3 folders selected")).toBeVisible();
      await expect(browser.getByRole("button", { name: "Use Current Folder" })).toHaveCount(0);
      await page.screenshot({ path: info.outputPath("selected-folders.png"), fullPage: true });
      await browser.getByRole("button", { name: "Add selected folders" }).click();
      await expect(
        form.getByRole("button", { name: `Remove ${first.name}`, exact: true }),
      ).toBeVisible();
      await expect(
        form.getByRole("button", { name: `Remove ${second.name}`, exact: true }),
      ).toBeVisible();
      await expect(
        form.getByRole("button", { name: `Remove ${third.name}`, exact: true }),
      ).toBeVisible();
      await page.screenshot({ path: info.outputPath("mixed-library-folders.png"), fullPage: true });
      await form.getByRole("button", { name: "Create Library", exact: true }).click();
      await expect.poll(() => saved.length).toBe(1);
      expect(saved[0]).toMatchObject({
        library: { name: "Archive library", paths: localPaths },
        selections: [first, second, third].map((dir) => ({
          source_key: source.source_key,
          configuration_revision: 1,
          selection_ref: dir.selection_ref,
        })),
      });
      await expect(form).toHaveCount(0);
      expect(unexpected).toEqual([]);
    });
  }
}

async function reply(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}
