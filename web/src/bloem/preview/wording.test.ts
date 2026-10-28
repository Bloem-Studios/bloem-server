import { describe, expect, it } from "vitest";
import { ctaMessage, itemCtaLabel } from "./wording";

const presentation = {
  kind: "guest" as const,
  ends_at: null,
  cta_url: "https://store.test/trial",
  cta_label: "Subscribe",
  item_cta_label: "",
  cta_message: "",
  subscribe_label: "",
  subscribe_url: "",
  storefront_url: "",
  launch_url: "",
};

describe("preview wording", () => {
  it("keeps the client's wording for each kind of title when the operator sets none", () => {
    expect(itemCtaLabel(presentation, "watch")).toBe("Subscribe to watch");
    expect(itemCtaLabel(presentation, "read")).toBe("Subscribe to read");
    expect(itemCtaLabel(presentation, "listen")).toBe("Subscribe to listen");
    expect(itemCtaLabel(null, "watch")).toBe("Subscribe to watch");
    expect(ctaMessage(presentation)).toBe(
      "Browse the library here. Subscribe to watch, read and listen.",
    );
  });
  it("uses the operator's wording for every kind of title", () => {
    const set = {
      ...presentation,
      item_cta_label: " Start your €1 trial ",
      cta_message: "Seven days, one euro.",
      subscribe_label: "",
      subscribe_url: "",
    };
    expect(itemCtaLabel(set, "watch")).toBe("Start your €1 trial");
    expect(itemCtaLabel(set, "listen")).toBe("Start your €1 trial");
    expect(ctaMessage(set)).toBe("Seven days, one euro.");
  });
  it("treats a presentation remembered before these fields existed as unset", () => {
    const old = { ...presentation } as Partial<typeof presentation>;
    delete old.item_cta_label;
    delete old.cta_message;
    expect(itemCtaLabel(old as typeof presentation, "read")).toBe("Subscribe to read");
    expect(ctaMessage(old as typeof presentation)).toBe(
      "Browse the library here. Subscribe to watch, read and listen.",
    );
  });
});
