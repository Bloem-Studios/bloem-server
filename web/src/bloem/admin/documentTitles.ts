import type { BloemMessageKey } from "@/bloem/i18n";
import type { MessageKey } from "@/i18n";

// Silo's admin titles are Silo message ids. Bloem's are Bloem ids, which
// documentTitle.ts formats through Bloem's i18n adapter (bloem-preview-plugin.ts).
function title(id: BloemMessageKey): { tab: MessageKey; header: MessageKey } {
  const key = id as unknown as MessageKey;
  return { tab: key, header: key };
}

/** The tab and phone-header title of Bloem's platform organization pages. */
export function bloemOrganizationsTitle(oneOrganization: boolean) {
  return title(
    oneOrganization ? "bloem.adminTitle.organization" : "bloem.adminTitle.organizations",
  );
}
