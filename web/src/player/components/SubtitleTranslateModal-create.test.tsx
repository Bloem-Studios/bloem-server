import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SubtitleTranslateModal } from "./SubtitleTranslateModal";
import type { PlayerConfig } from "../context/PlayerConfigContext";
import type { PlayerSubtitleInfo } from "../types";

const mocks = vi.hoisted(() => ({ v2: vi.fn(), info: vi.fn() }));
vi.mock("../player-v2", () => ({ playerV2: mocks.v2 }));
vi.mock("sonner", () => ({ toast: { info: mocks.info } }));
afterEach(() => {
  cleanup();
  mocks.v2.mockReset();
  mocks.info.mockReset();
});
const tracks: PlayerSubtitleInfo[] = [
  { index: 3, language: "en", label: "English", url: "", source: "external", codec: "srt" },
];
const response = {
  job: {
    id: "9007199254740993",
    media_file_id: "42",
    kind: "translate",
    source_index: 3,
    status: "pending",
  },
  live_delivery_attached: false,
};

it.each(["profile", "token", "pin", "authority", "file", "session", "close"])(
  "discards decoded AI creation after %s changes",
  async (change) => {
    let profile = "viewer",
      token = "synthetic",
      pin = "pin",
      authority = true;
    const config: PlayerConfig = {
      apiBaseUrl: "/api/v1",
      getAccessToken: () => token,
      getProfileId: () => profile,
      getProfileToken: () => pin,
      getDeviceId: () => "device",
      capturePlaybackMutationContext: () => ({
        accountId: "7",
        profileId: profile,
        origin: "",
        isCurrent: () => authority,
      }),
    };
    let finish!: (value: unknown) => void;
    const pending = new Promise((resolve) => {
      finish = resolve;
    });
    mocks.v2.mockReturnValue(pending);
    const onClose = vi.fn();
    const props = {
      playerConfig: config,
      mediaFileId: 42,
      sessionId: "session",
      isOpen: true,
      onClose,
      tracks,
    };
    const view = render(<SubtitleTranslateModal {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "Translate" }));
    fireEvent.click(screen.getByRole("button", { name: /Starting/ }));
    expect(mocks.v2).toHaveBeenCalledTimes(1);
    expect(mocks.v2).toHaveBeenCalledWith(config, "POST /api/v2/subtitles/ai/translate", {
      body: {
        media_file_id: "42",
        kind: "translate",
        source_index: 3,
        source_language: "en",
        target_language: "en",
        session_id: "session",
        start_position: 0,
      },
    });
    if (change === "profile") profile = "other";
    else if (change === "token") token = "other";
    else if (change === "pin") pin = "other";
    else if (change === "authority") authority = false;
    else if (change === "file")
      view.rerender(<SubtitleTranslateModal {...props} mediaFileId={99} />);
    else if (change === "session")
      view.rerender(<SubtitleTranslateModal {...props} sessionId="successor" />);
    else view.rerender(<SubtitleTranslateModal {...props} isOpen={false} />);
    await act(async () => {
      finish(response);
      await pending;
    });
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.info).not.toHaveBeenCalled();
    expect(mocks.v2).toHaveBeenCalledTimes(1);
  },
);

it.each([false, true])(
  "validates exact returned job binding (invalid=%s) without retry",
  async (invalid) => {
    const config: PlayerConfig = {
      apiBaseUrl: "/api/v1",
      getAccessToken: () => "synthetic",
      getProfileId: () => "viewer",
      getDeviceId: () => "device",
    };
    mocks.v2.mockResolvedValue(
      invalid ? { ...response, job: { ...response.job, media_file_id: "99" } } : response,
    );
    const onClose = vi.fn();
    render(
      <SubtitleTranslateModal
        playerConfig={config}
        mediaFileId={42}
        sessionId="session"
        isOpen
        onClose={onClose}
        tracks={tracks}
      />,
    );
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Translate" }));
    });
    expect(mocks.v2).toHaveBeenCalledTimes(1);
    if (invalid) {
      expect(screen.getByText("Subtitle processing returned an invalid job.")).toBeTruthy();
      expect(onClose).not.toHaveBeenCalled();
    } else {
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(mocks.info).toHaveBeenCalledTimes(1);
    }
  },
);
