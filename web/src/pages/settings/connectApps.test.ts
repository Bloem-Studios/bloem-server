import { describe, expect, it } from "vitest";
import * as apps from "./connectApps";

describe("native app connection choices", () => {
  it("offers native apps without removed protocol choices", () => {
    expect(apps.SILO_APP_EXAMPLES).toContain("Android");
    expect(apps).not.toHaveProperty("JELLYFIN_APP_EXAMPLES");
    expect(apps).not.toHaveProperty("buildJellyfinUsername");
  });
});
