import { useState, type FormEvent } from "react";
import {
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import type { LiveTVChannel } from "@/api/bloemTypes";
import { useBloemT } from "@/bloem/i18n";
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
  const bt = useBloemT();
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
      setError(bt("bloem.liveTv.manualRecording.invalidTimes"));
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
      setNotice(bt("bloem.liveTv.manualRecording.scheduled"));
      setTitle("");
    } catch (cause) {
      if (isCapturedProfileAuthorityActive(authority))
        setError(
          bt("bloem.liveTv.manualRecording.failed", {
            reason:
              cause instanceof Error
                ? cause.message
                : bt("bloem.liveTv.manualRecording.couldNotConfirm"),
          }),
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
        setNotice(bt("bloem.liveTv.manualRecording.reloaded"));
      }
    } catch {
      if (isCapturedProfileAuthorityActive(authority))
        setError(bt("bloem.liveTv.manualRecording.reloadFailed"));
    }
  }

  return (
    <details className="border-border rounded-xl border p-4">
      <summary className="cursor-pointer font-medium">
        {bt("bloem.liveTv.manualRecording.summary")}
      </summary>
      <form className="mt-4 space-y-4" onSubmit={(event) => void submit(event)}>
        <p className="text-muted-foreground text-sm">
          {bt("bloem.liveTv.manualRecording.intro", {
            timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
          })}
        </p>
        <fieldset disabled={schedule.isBlocked} className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="manual-recording-title">
              {bt("bloem.liveTv.manualRecording.title")}
            </Label>
            <Input
              id="manual-recording-title"
              required
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="manual-recording-channel">
              {bt("bloem.liveTv.manualRecording.channel")}
            </Label>
            <select
              id="manual-recording-channel"
              required
              value={channel}
              onChange={(event) => setChannel(event.target.value)}
              className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
            >
              <option value="">{bt("bloem.liveTv.manualRecording.chooseChannel")}</option>
              {channels.map((item) => (
                <option key={item.id} value={item.id}>
                  {channelLabel(item)}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="manual-recording-start">
              {bt("bloem.liveTv.manualRecording.startsAt")}
            </Label>
            <Input
              id="manual-recording-start"
              type="datetime-local"
              required
              value={start}
              onChange={(event) => setStart(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="manual-recording-stop">
              {bt("bloem.liveTv.manualRecording.endsAt")}
            </Label>
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
            <p>{bt("bloem.liveTv.manualRecording.changeUnconfirmed")}</p>
            <Button
              type="button"
              variant="outline"
              disabled={schedule.isPending}
              onClick={() => void reload()}
            >
              {schedule.isPending
                ? bt("bloem.liveTv.reloadingRecordings")
                : bt("bloem.liveTv.reloadRecordings")}
            </Button>
          </div>
        )}
        <Button
          type="submit"
          disabled={schedule.isBlocked || !channel || !title.trim() || !start || !stop}
        >
          {schedule.isPending
            ? bt("bloem.liveTv.manualRecording.checking")
            : bt("bloem.liveTv.manualRecording.submit")}
        </Button>
      </form>
    </details>
  );
}
