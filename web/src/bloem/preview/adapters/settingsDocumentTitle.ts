import { bloemT } from "@/bloem/i18n";
import { isSourceCodeSettingsPath } from "@/bloem/sourceCode/api";
import { resolveSettingsDocumentTitle as siloTitle } from "@/lib/documentTitle";

export * from "@/lib/documentTitle";

/**
 * The personal settings layout's tab title, with Bloem's Source code page
 * named; Silo's table would call it "Settings" (bloem-preview-plugin.ts).
 */
export function resolveSettingsDocumentTitle(pathname: string): string {
  return isSourceCodeSettingsPath(pathname)
    ? bloemT("bloem.sourceCode.title")
    : siloTitle(pathname);
}
