import { bloemT } from "@/bloem/i18n";
import type { PreviewPresentation } from "./session";

// The operator's wording when they set one, otherwise the client's own. A
// presentation remembered from before the server carried these fields lacks
// them, which reads the same as unset.
type Presentation = PreviewPresentation | null | undefined;

function subscribeTo(verb: "watch" | "read" | "listen") {
  switch (verb) {
    case "read":
      return bloemT("bloem.preview.cta.subscribeToRead");
    case "listen":
      return bloemT("bloem.preview.cta.subscribeToListen");
    default:
      return bloemT("bloem.preview.cta.subscribeToWatch");
  }
}

/** The button that stands where play would be: on title pages and in the player. */
export function itemCtaLabel(presentation: Presentation, verb: "watch" | "read" | "listen") {
  return (presentation?.item_cta_label ?? "").trim() || subscribeTo(verb);
}

/** The sentence in the subscribe dialog. */
export function ctaMessage(presentation: Presentation) {
  return (presentation?.cta_message ?? "").trim() || bloemT("bloem.preview.cta.message");
}
