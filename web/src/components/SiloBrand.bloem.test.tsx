// @vitest-environment jsdom

import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { BrandingContext, type BrandingContextValue } from "@/contexts/BrandingProvider";
import { SiloBrand, type SiloBrandVariant } from "./SiloBrand";

const BRANDING_DEFAULTS: BrandingContextValue = {
  serverName: "Bloem",
  loginSubtitle: "Sign in with an existing account.",
  accentColor: null,
  wordmarkUrl: null,
  markUrl: null,
  faviconUrl: null,
  loginBgUrl: null,
  storageAvailable: false,
};

function renderBrandSrc(variant: SiloBrandVariant): string | null {
  const markup = renderToStaticMarkup(
    <BrandingContext value={BRANDING_DEFAULTS}>
      <SiloBrand variant={variant} />
    </BrandingContext>,
  );
  return (
    new DOMParser()
      .parseFromString(markup, "text/html")
      .querySelector("img")
      ?.getAttribute("src") ?? null
  );
}

describe("Bloem built-in branding", () => {
  it("uses the white-text built-in wordmark", () => {
    expect(renderBrandSrc("wordmark")).toBe("/bloem-wordmark-sidebar.png");
  });

  it("uses the built-in mark with no custom asset", () => {
    expect(renderBrandSrc("mark")).toBe("/bloem-icon-1024.png");
  });
});
