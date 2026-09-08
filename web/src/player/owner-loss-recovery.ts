import { readBoundAcceptedProgress, type ProgressTimeline } from "./bound-client-timeline";

/** The journal is terminal, but automatic continuations must not treat abandonment as STOP success. */
export class PlaybackOwnerLostError extends Error {
  constructor() {
    super("Playback ended after its server owner was lost. Start playback again explicitly.");
    this.name = "PlaybackOwnerLostError";
  }
}

export type OwnerLossRecovery = {
  recovery_id: string;
  playback_attempt_id: string;
  session_id: string;
  state: "draining" | "aborted";
  reason: "owner_lost";
  accepted?: {
    sequence: number;
    position: number;
    is_paused: boolean;
    timeline_id?: string;
    item_position?: number;
  };
};

type ExpectedRecovery = {
  attemptId: string | undefined;
  sessionId?: string;
  timeline?: Readonly<ProgressTimeline>;
  prior?: OwnerLossRecovery;
};
const owns = (value: object, field: string) => Object.prototype.hasOwnProperty.call(value, field);
const nonempty = (value: unknown): value is string => typeof value === "string" && value.length > 0;
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Playback recovery returned an invalid receipt");
  return value as Record<string, unknown>;
}

/** A separate terminal receipt; it never confirms the client's uncommitted final sample. */
export function readOwnerLossRecovery(
  value: unknown,
  status: number,
  operation: "start" | "stop",
  expected: ExpectedRecovery,
): OwnerLossRecovery | undefined {
  const receipt = object(value);
  if (!owns(receipt, "recovery")) return;
  const recovery = object(receipt.recovery);
  if (
    !nonempty(expected.attemptId) ||
    recovery.playback_attempt_id !== expected.attemptId ||
    !nonempty(recovery.recovery_id) ||
    !nonempty(recovery.session_id) ||
    (expected.sessionId !== undefined && recovery.session_id !== expected.sessionId) ||
    recovery.reason !== "owner_lost" ||
    (recovery.state !== "draining" && recovery.state !== "aborted") ||
    ["session_id", "playback_plan", "progress_timeline", "stop_id", "accepted", "history_id"].some(
      (field) => owns(receipt, field),
    )
  )
    throw new Error("Playback recovery does not match the original attempt");
  if (
    expected.prior &&
    (expected.prior.recovery_id !== recovery.recovery_id ||
      expected.prior.playback_attempt_id !== recovery.playback_attempt_id ||
      expected.prior.session_id !== recovery.session_id ||
      expected.prior.reason !== recovery.reason ||
      (expected.prior.state !== "draining" && expected.prior.state !== recovery.state))
  )
    throw new Error("Playback recovery identity changed");
  if (recovery.state === "draining") {
    if (
      status !== 202 ||
      receipt.outcome !== "draining" ||
      owns(receipt, "terminal") ||
      owns(recovery, "accepted")
    )
      throw new Error("Playback recovery is not a valid pending receipt");
  } else if (operation === "start") {
    const terminal = object(receipt.terminal);
    if (
      status !== 201 ||
      receipt.protocol_version !== 3 ||
      !Array.isArray(receipt.server_features) ||
      !receipt.server_features.every((feature) => typeof feature === "string") ||
      receipt.outcome !== "adaptation_unavailable" ||
      terminal.reason !== "playback_owner_lost" ||
      terminal.retryable !== false ||
      !nonempty(terminal.message)
    )
      throw new Error("Playback recovery returned an unconfirmed terminal decision");
  } else if (status !== 200 || receipt.outcome !== "aborted" || owns(receipt, "terminal")) {
    throw new Error("Playback recovery returned an unconfirmed abandonment");
  }
  const result: OwnerLossRecovery = {
    recovery_id: recovery.recovery_id,
    playback_attempt_id: expected.attemptId,
    session_id: recovery.session_id,
    state: recovery.state,
    reason: "owner_lost",
  };
  if (owns(recovery, "accepted")) {
    const accepted = object(recovery.accepted);
    if (
      typeof accepted.sequence !== "number" ||
      !Number.isSafeInteger(accepted.sequence) ||
      accepted.sequence < 1 ||
      typeof accepted.position !== "number" ||
      !Number.isFinite(accepted.position) ||
      accepted.position < 0 ||
      typeof accepted.is_paused !== "boolean"
    )
      throw new Error("Playback recovery returned invalid accepted progress");
    if (expected.timeline) result.accepted = readBoundAcceptedProgress(accepted, expected.timeline);
    else {
      if (owns(accepted, "timeline_id") || owns(accepted, "item_position"))
        throw new Error("Playback recovery returned an unexpected timeline");
      result.accepted = {
        sequence: accepted.sequence,
        position: accepted.position,
        is_paused: accepted.is_paused,
      };
    }
  }
  if (
    expected.prior?.state === "aborted" &&
    JSON.stringify(expected.prior) !== JSON.stringify(result)
  )
    throw new Error("Playback recovery terminal receipt changed");
  return result;
}
