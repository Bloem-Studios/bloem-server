// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CampaignEditor, SeasonEditor } from "./EngagementEditors";
import { campaignImage, campaignLink, type StoredCampaign } from "./campaigns";

const campaign: StoredCampaign = {
  id: "campaign",
  organization_id: null,
  surfaces: ["home"],
  placement: { home_position: 1 },
  kicker: "Server news",
  headline: "Winter collection",
  subtitle: "Ready to watch",
  image_url: "https://assets.example/card.webp",
  deeplink: "/collections/winter",
  cta: null,
  priority: 1,
  starts_at: "2099-12-01T00:00:00Z",
  ends_at: "2100-01-01T00:00:00Z",
  targeting: { audience: "all" },
  dismissible: true,
  updated_at: "2099-01-01T00:00:00Z",
};
afterEach(cleanup);
describe("typed campaign authoring", () => {
  it("requires review and preserves typed targeting, schedule and placement", async () => {
    const save = vi.fn().mockResolvedValue(undefined);
    render(
      <CampaignEditor
        initial={campaign}
        pending={false}
        storageAvailable={false}
        upload={vi.fn()}
        onCancel={vi.fn()}
        onSave={save}
      />,
    );
    expect(screen.queryByRole("button", { name: "Save campaign changes" })).toBeNull();
    fireEvent.change(screen.getByLabelText("Audience type"), { target: { value: "library" } });
    fireEvent.change(screen.getByLabelText("Audience library ID"), { target: { value: "42" } });
    fireEvent.click(screen.getByRole("button", { name: "Review campaign" }));
    expect(screen.getByRole("region", { name: "Campaign review" })).toHaveTextContent(
      "Audience: library",
    );
    expect(save).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Save campaign changes" }));
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        targeting: { audience: "library", library_id: 42 },
        starts_at: "2099-12-01T00:00:00.000Z",
        ends_at: "2100-01-01T00:00:00.000Z",
        placement: expect.objectContaining({ home_position: 1 }),
      }),
    );
  });
  it("invalidates review after changing the draft and forbids non-dismissible overlays", () => {
    render(
      <CampaignEditor
        initial={campaign}
        pending={false}
        storageAvailable={false}
        upload={vi.fn()}
        onCancel={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Review campaign" }));
    fireEvent.change(screen.getByLabelText("Headline"), { target: { value: "Updated" } });
    expect(screen.queryByRole("region", { name: "Campaign review" })).toBeNull();
    fireEvent.click(screen.getByLabelText("in playback"));
    fireEvent.click(screen.getByLabelText("Allow viewers to dismiss"));
    fireEvent.click(screen.getByRole("button", { name: "Review campaign" }));
    expect(screen.getByRole("alert")).toHaveTextContent("In-playback campaigns require");
  });
  it("rejects unsafe artwork before offering publication", () => {
    render(
      <CampaignEditor
        initial={{ ...campaign, image_url: "javascript:alert(1)" }}
        pending={false}
        storageAvailable={false}
        upload={vi.fn()}
        onCancel={vi.fn()}
        onSave={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Review campaign" }));
    expect(screen.getByRole("alert")).toHaveTextContent("HTTPS artwork");
    expect(screen.queryByRole("region", { name: "Campaign review" })).toBeNull();
  });
});
it("reviews annual seasonal schedules and uploads artwork through the supplied adapter", async () => {
  const save = vi.fn().mockResolvedValue(undefined),
    upload = vi.fn().mockResolvedValue("/api/v1/ambience/assets/banner.webp");
  render(
    <SeasonEditor
      pending={false}
      storageAvailable
      yearlyAvailable
      upload={upload}
      onCancel={vi.fn()}
      onSave={save}
    />,
  );
  fireEvent.change(screen.getByLabelText("Starts at (UTC)"), {
    target: { value: "2099-12-01T00:00" },
  });
  fireEvent.change(screen.getByLabelText("Ends at (UTC)"), {
    target: { value: "2100-01-01T00:00" },
  });
  fireEvent.click(screen.getByLabelText("Repeat every year"));
  const file = new File(["image"], "banner.webp", { type: "image/webp" });
  fireEvent.change(screen.getByLabelText(/Upload artwork/), { target: { files: [file] } });
  await waitFor(() =>
    expect(screen.getByLabelText("Artwork URL")).toHaveValue("/api/v1/ambience/assets/banner.webp"),
  );
  expect(upload).toHaveBeenCalledWith(file, "season_banner");
  fireEvent.click(screen.getByRole("button", { name: "Review seasonal pack" }));
  fireEvent.click(screen.getByRole("button", { name: "Publish seasonal pack" }));
  expect(save).toHaveBeenCalledWith(
    expect.objectContaining({
      window: {
        starts_at: "2099-12-01T00:00:00.000Z",
        ends_at: "2100-01-01T00:00:00.000Z",
        repeat_yearly: true,
        timezone: "UTC",
      },
      assets: { banner_url: "/api/v1/ambience/assets/banner.webp", sprites: [] },
    }),
  );
});
it.each([
  "javascript:alert(1)",
  "//foreign.example",
  "https://user:pass@example.test/image",
  "/\\foreign.example",
  "https://example.test/\nimage",
])("rejects unsafe author-controlled links: %s", (url) => {
  expect(campaignLink(url)).toBeNull();
  expect(campaignImage(url)).toBeNull();
});
it("accepts HTTPS artwork and bounded local asset paths, not arbitrary authenticated endpoints", () => {
  expect(campaignImage("/api/v1/ambience/assets/banner.webp")).toBeTruthy();
  expect(campaignImage("/api/v1/users/private-avatar")).toBeNull();
  expect(campaignLink("/collections/winter")).toBe("/collections/winter");
});
