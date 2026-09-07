import { useEffect, useRef, useState } from "react";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import {
  mintPlaybackControlSocketTicket,
  playbackControlSocketProtocols,
  playbackControlSocketURL,
} from "@/api/v2/playbackControlSocket";
import { V2ProblemError } from "@/api/v2/request";
import { usePlayerConfig } from "../context/PlayerConfigContext";
import { sessionInstallation } from "../session-mutations";
import {
  buildPlaybackRealtimeAck,
  buildPlaybackRealtimeHello,
  buildPlaybackRealtimeResult,
  parsePlaybackRealtimeMessage,
  type PlaybackCommandName,
  type PlaybackRealtimeCommandEnvelope,
  type PlaybackRealtimeEventEnvelope,
} from "../realtime-protocol";

type ConnectionState = "disconnected" | "connecting" | "connected";

interface UsePlaybackRealtimeOptions {
  sessionId: string | null;
  onCommand: (command: PlaybackRealtimeCommandEnvelope) => Promise<void> | void;
  onEvent?: (event: PlaybackRealtimeEventEnvelope) => void;
  /**
   * The commands this surface can execute, announced in the hello. Defaults to
   * the shared set; a surface that handles more names them so it does not
   * announce a command it would only reject.
   */
  supportedCommands?: PlaybackCommandName[];
}

interface UsePlaybackRealtimeResult {
  connectionState: ConnectionState;
}

const reconnectDelays = [500, 1_000, 2_000, 5_000];

/**
 * The v2 handshake is the owner-bound path: one single-use credential per
 * connection, minted under the profile authority captured for that attempt.
 * The bridge socket remains only for a server that does not serve the v2
 * handshake (404/503 on the ticket), never as a retry after a refusal.
 */
export function isPlaybackControlFallbackError(error: unknown): boolean {
  return (
    error instanceof V2ProblemError &&
    (error.problemType === "not_found" || error.problemType === "dependency_unavailable")
  );
}

export function createPlaybackRealtimeUrlFactory(
  apiBaseUrl: string,
  sessionId: string,
  getAccessToken: () => string | null,
): () => string {
  const wsBase = apiBaseUrl.replace(/^http/, "ws");
  return () => {
    const token = getAccessToken();
    return `${wsBase}/playback/sessions/${sessionId}/control/ws${token ? `?token=${token}` : ""}`;
  };
}

export function usePlaybackRealtime({
  sessionId,
  onCommand,
  onEvent,
  supportedCommands,
}: UsePlaybackRealtimeOptions): UsePlaybackRealtimeResult {
  const config = usePlayerConfig();
  const [connectionState, setConnectionState] = useState<ConnectionState>("disconnected");
  const onCommandRef = useRef(onCommand);
  const onEventRef = useRef(onEvent);
  const supportedCommandsRef = useRef(supportedCommands);
  const seenCommandsRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    onCommandRef.current = onCommand;
  }, [onCommand]);

  useEffect(() => {
    supportedCommandsRef.current = supportedCommands;
  }, [supportedCommands]);

  useEffect(() => {
    onEventRef.current = onEvent;
  }, [onEvent]);

  useEffect(() => {
    if (!sessionId) {
      seenCommandsRef.current.clear();
      return;
    }

    const getWsUrl = createPlaybackRealtimeUrlFactory(
      config.apiBaseUrl,
      sessionId,
      config.getAccessToken,
    );

    let disposed = false;
    let attempt = 0;
    let socket: WebSocket | null = null;
    let reconnectTimer: number | null = null;

    const scheduleReconnect = () => {
      if (disposed) return;
      const delay = reconnectDelays[Math.min(attempt, reconnectDelays.length - 1)];
      attempt += 1;
      reconnectTimer = window.setTimeout(connect, delay);
    };

    let useBridge = false;
    const attach = (opened: WebSocket, authority: ProfileRequestContextSnapshot | null) => {
      socket = opened;
      const authorityActive = () =>
        authority === null || isCapturedProfileAuthorityActive(authority);

      socket.addEventListener("open", () => {
        if (!socket || socket.readyState !== WebSocket.OPEN) return;
        if (!authorityActive()) {
          socket.close();
          return;
        }
        attempt = 0;
        setConnectionState("connected");
        seenCommandsRef.current.clear();
        socket.send(
          JSON.stringify(buildPlaybackRealtimeHello(sessionId, supportedCommandsRef.current)),
        );
      });

      socket.addEventListener("message", (event) => {
        // Frames that arrive after the captured account/profile authority
        // changed belong to a session this browser no longer owns.
        if (!authorityActive()) {
          socket?.close();
          return;
        }
        const message = parsePlaybackRealtimeMessage(String(event.data));
        if (!message || message.session_id !== sessionId || !socket) {
          return;
        }
        if (message.type === "event") {
          onEventRef.current?.(message);
          return;
        }

        const command = message;
        if (seenCommandsRef.current.has(command.command_id)) {
          return;
        }
        seenCommandsRef.current.add(command.command_id);

        if (socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify(buildPlaybackRealtimeAck(sessionId, command.command_id)));
        }

        void Promise.resolve(onCommandRef.current(command))
          .then(() => {
            if (!socket || socket.readyState !== WebSocket.OPEN) return;
            socket.send(
              JSON.stringify(
                buildPlaybackRealtimeResult(sessionId, command.command_id, "completed"),
              ),
            );
          })
          .catch((error: unknown) => {
            if (!socket || socket.readyState !== WebSocket.OPEN) return;
            const message = error instanceof Error ? error.message : "command_failed";
            socket.send(
              JSON.stringify(
                buildPlaybackRealtimeResult(sessionId, command.command_id, "rejected", message),
              ),
            );
          });
      });

      socket.addEventListener("close", () => {
        setConnectionState("disconnected");
        socket = null;
        scheduleReconnect();
      });

      socket.addEventListener("error", () => {
        socket?.close();
      });
    };

    const connect = () => {
      if (disposed) return;
      setConnectionState("connecting");

      const authority = useBridge ? null : captureProfileRequestContext();
      if (!authority) {
        // No captured profile authority (or the v2 handshake is not served):
        // the bridge socket carries the current token on every attempt.
        try {
          attach(new WebSocket(getWsUrl()), null);
        } catch {
          scheduleReconnect();
        }
        return;
      }

      void mintPlaybackControlSocketTicket(sessionId, sessionInstallation(sessionId), authority)
        .then((ticket) => {
          if (disposed || !isCapturedProfileAuthorityActive(authority)) return;
          try {
            attach(
              new WebSocket(
                playbackControlSocketURL(sessionId, config.socketOrigin ?? window.location.origin),
                playbackControlSocketProtocols(ticket.ticket),
              ),
              authority,
            );
          } catch {
            scheduleReconnect();
          }
        })
        .catch((error: unknown) => {
          if (disposed) return;
          if (error instanceof StaleApiRequestContextError) {
            // The account or profile changed underneath this player; nothing
            // this browser can mint is valid for the session any more.
            setConnectionState("disconnected");
            return;
          }
          if (isPlaybackControlFallbackError(error)) {
            useBridge = true;
            connect();
            return;
          }
          // A refused mint (403 non-owner, 409 stale lease or held lane) is
          // not retried blindly; the bounded backoff re-captures authority.
          setConnectionState("disconnected");
          scheduleReconnect();
        });
    };

    connect();

    return () => {
      disposed = true;
      setConnectionState("disconnected");
      if (reconnectTimer !== null) {
        window.clearTimeout(reconnectTimer);
      }
      if (
        socket &&
        (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)
      ) {
        socket.close();
      }
    };
  }, [config, sessionId]);

  return { connectionState };
}
