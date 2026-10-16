import type { PreviewPresentation } from "./session";

// The operator's wording when they set one, otherwise the client's own. A
// presentation remembered from before the server carried these fields lacks
// them, which reads the same as unset.
type Presentation = PreviewPresentation | null | undefined;

/** The button that stands where play would be: on title pages and in the player. */
export function itemCtaLabel(presentation: Presentation, verb: "watch" | "read" | "listen") {
  return (presentation?.item_cta_label ?? "").trim() || `Subscribe to ${verb}`;
}

/** The sentence in the subscribe dialog. */
export function ctaMessage(presentation: Presentation) {
  return (
    (presentation?.cta_message ?? "").trim() ||
    "Browse the library here. Subscribe to watch, read and listen."
  );
}
