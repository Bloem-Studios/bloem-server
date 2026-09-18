import { useState, type FormEvent } from "react";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import type { LiveTVChannel } from "@/api/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/hooks/useAuth";
import { useScheduleLiveTVRecording } from "@/hooks/queries/useLiveTV";
import { channelLabel } from "@/lib/liveTVGuide";

export function ManualRecordingForm({ channels }: { channels: LiveTVChannel[] }) {
  const { profile } = useAuth();
  const authority = captureProfileRequestContext();
  if (!authority || authority.profileId !== profile?.id) return null;
  return (
    <RecordingForm
      key={JSON.stringify([
        authority.serverOrigin,
        authority.authContextVersion,
        authority.profileId,
        authority.profileTokenGeneration,
      ])}
      channels={channels}
      authority={authority}
    />
  );
}

function RecordingForm({
  channels,
  authority,
}: {
  channels: LiveTVChannel[];
  authority: ProfileRequestContextSnapshot;
}) {
  const schedule = useScheduleLiveTVRecording(authority);
  const [channel, setChannel] = useState("");
  const [title, setTitle] = useState("");
  const [start, setStart] = useState("");
  const [stop, setStop] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (schedule.isBlocked) return;
    const begin = new Date(start),
      end = new Date(stop);
    if (!authority || !isCapturedProfileAuthorityActive(authority)) return;
    if (
      !Number.isFinite(begin.getTime()) ||
      !Number.isFinite(end.getTime()) ||
      end <= begin ||
      end.getTime() <= Date.now()
    ) {
      setError("Choose an end time after the start time and in the future.");
      return;
    }
    if (!title.trim() || !channels.some((item) => item.id === channel)) return;
    setError("");
    setNotice("");
    try {
      await schedule.mutateAsync({
        channel_id: channel,
        title: title.trim(),
        start: begin.toISOString(),
        stop: end.toISOString(),
      });
      if (!isCapturedProfileAuthorityActive(authority)) return;
      setNotice("Recording scheduled.");
      setTitle("");
    } catch (cause) {
      if (isCapturedProfileAuthorityActive(authority))
        setError(
          `${cause instanceof Error ? cause.message : "Could not confirm the recording."} Review recording status before trying again.`,
        );
    }
  }
  async function reload() {
    setError("");
    setNotice("");
    try {
      await schedule.reloadRecordings();
      if (isCapturedProfileAuthorityActive(authority)) {
        setTitle("");
        setNotice(
          "Recordings reloaded. Review the list and enter a title to schedule a new recording.",
        );
      }
    } catch {
      if (isCapturedProfileAuthorityActive(authority))
        setError("Could not reload recordings. Recording actions remain blocked; try again.");
    }
  }

  return (
    <details className="border-border rounded-xl border p-4">
      <summary className="cursor-pointer font-medium">Schedule by channel and time</summary>
      <form className="mt-4 space-y-4" onSubmit={(event) => void submit(event)}>
        <p className="text-muted-foreground text-sm">
          Use this when a program is missing from the guide. Times use your browser’s timezone (
          {Intl.DateTimeFormat().resolvedOptions().timeZone}).
        </p>
        <fieldset disabled={schedule.isBlocked} className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="manual-recording-title">Recording title</Label>
            <Input
              id="manual-recording-title"
              required
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="manual-recording-channel">Recording channel</Label>
            <select
              id="manual-recording-channel"
              required
              value={channel}
              onChange={(event) => setChannel(event.target.value)}
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
            >
              <option value="">Choose a channel</option>
              {channels.map((item) => (
                <option key={item.id} value={item.id}>
                  {channelLabel(item)}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="manual-recording-start">Starts at</Label>
            <Input
              id="manual-recording-start"
              type="datetime-local"
              required
              value={start}
              onChange={(event) => setStart(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="manual-recording-stop">Ends at</Label>
            <Input
              id="manual-recording-stop"
              type="datetime-local"
              required
              value={stop}
              onChange={(event) => setStop(event.target.value)}
            />
          </div>
        </fieldset>
        {error && (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        )}
        {notice && <p role="status">{notice}</p>}
        {schedule.needsReload && (
          <div role="alert" className="space-y-2">
            <p>Recording status is unknown. Reload and review recordings before another action.</p>
            <Button
              type="button"
              variant="outline"
              disabled={schedule.isPending}
              onClick={() => void reload()}
            >
              {schedule.isPending ? "Reloading recordings…" : "Reload recordings"}
            </Button>
          </div>
        )}
        <Button
          type="submit"
          disabled={schedule.isBlocked || !channel || !title.trim() || !start || !stop}
        >
          {schedule.isPending ? "Checking recordings…" : "Schedule recording"}
        </Button>
      </form>
    </details>
  );
}
