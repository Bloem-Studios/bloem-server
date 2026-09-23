import { useId, useState, type FormEvent } from "react";
import { captureProfileRequestContext } from "@/api/client";
import type { LiveTVChannel, LiveTVProgram, LiveTVSeriesRule } from "@/api/bloemTypes";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useAuth } from "@/hooks/useAuth";
import {
  useCreateLiveTVSeriesRule,
  useDeleteLiveTVSeriesRule,
  useLiveTVSeriesRules,
} from "@/hooks/queries/useLiveTVSeriesRules";
import { channelLabel } from "@/lib/liveTVGuide";

type SeriesRuleProps = {
  channels: LiveTVChannel[];
  programs?: LiveTVProgram[];
  selectedProgram?: LiveTVProgram | null;
};

export function LiveTVSeriesRules({ channels, programs = [], selectedProgram }: SeriesRuleProps) {
  const { profile } = useAuth();
  const authority = captureProfileRequestContext();
  if (!authority || authority.profileId !== profile?.id) {
    return <p role="alert">Select a profile to manage recording rules.</p>;
  }
  // Drafts and confirmations must not survive a profile, login, server or PIN change.
  return (
    <SeriesRulesContent
      key={JSON.stringify([
        authority.serverOrigin,
        authority.authContextVersion,
        profile.id,
        authority.profileTokenGeneration,
        selectedProgram?.id,
      ])}
      channels={channels}
      programs={programs}
      selectedProgram={selectedProgram}
    />
  );
}

function ruleLabel(rule: LiveTVSeriesRule) {
  return (
    rule.title_match || (rule.series_id ? `Series ${rule.series_id}` : "All programs on channel")
  );
}

function SeriesRulesContent({ channels, programs = [], selectedProgram }: SeriesRuleProps) {
  const fieldId = useId();
  const rules = useLiveTVSeriesRules();
  const create = useCreateLiveTVSeriesRule();
  const remove = useDeleteLiveTVSeriesRule();
  const [title, setTitle] = useState(selectedProgram?.title ?? "");
  const [seriesId, setSeriesId] = useState(selectedProgram?.series_id ?? "");
  const [channelId, setChannelId] = useState(selectedProgram?.channel_id ?? "");
  const series = new Map(
    [...programs, ...(selectedProgram ? [selectedProgram] : [])]
      .filter((program) => program.series_id)
      .map((program) => [program.series_id, program.title]),
  );
  const [newOnly, setNewOnly] = useState(false);
  const [confirmRule, setConfirmRule] = useState<LiveTVSeriesRule | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const busy = create.isPending || remove.isPending;
  const unavailable = busy || rules.isFetching || !rules.isSuccess;
  const validChannel = !channelId || channels.some((channel) => channel.id === channelId);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (unavailable || (!seriesId && !title.trim()) || !validChannel) return;
    setError(null);
    setNotice(null);
    try {
      await create.mutateAsync({
        title_match: seriesId ? "" : title.trim(),
        ...(seriesId ? { series_id: seriesId } : {}),
        ...(channelId ? { channel_id: channelId } : {}),
        new_only: newOnly,
      });
      setTitle("");
      setNotice("Recording rule created. Matching guide programs will be scheduled by the server.");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not create the recording rule.");
    }
  }

  async function deleteRule() {
    if (!confirmRule || unavailable) return;
    setError(null);
    setNotice(null);
    try {
      await remove.mutateAsync(confirmRule.id);
      setNotice("Rule removed. Already scheduled recordings and recorded files are unchanged.");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not remove the recording rule.");
    } finally {
      setConfirmRule(null);
    }
  }

  return (
    <section className="space-y-6" aria-labelledby={`${fieldId}-heading`}>
      <div className="space-y-2">
        <h2 id={`${fieldId}-heading`} className="text-xl font-semibold">
          Series recording rules
        </h2>
        <p className="text-muted-foreground max-w-2xl text-sm">
          Record an exact guide series, or match titles containing your search text (ignoring letter
          case). Narrow the rule to one channel or match across all channels.
        </p>
      </div>
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
      {notice && (
        <p role="status" className="text-muted-foreground text-sm">
          {notice}
        </p>
      )}
      {rules.isError && (
        <div role="alert" className="space-y-3">
          <p className="text-destructive text-sm">{rules.error.message}</p>
          <Button variant="outline" onClick={() => void rules.refetch()}>
            Reload recording rules
          </Button>
        </div>
      )}
      <form onSubmit={(event) => void submit(event)} aria-label="Create recording rule">
        <fieldset disabled={busy} className="border-border space-y-4 rounded-xl border p-4">
          <legend className="px-2 font-medium">Create a recording rule</legend>
          {series.size > 0 && (
            <div className="space-y-2">
              <Label htmlFor={`${fieldId}-series`}>Match guide series</Label>
              <select
                id={`${fieldId}-series`}
                value={seriesId}
                onChange={(event) => setSeriesId(event.target.value)}
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              >
                <option value="">Use title matching instead</option>
                {Array.from(series, ([id, name]) => (
                  <option key={id} value={id}>
                    {name}
                  </option>
                ))}
              </select>
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor={`${fieldId}-title`}>Title contains</Label>
              <Input
                id={`${fieldId}-title`}
                value={title}
                disabled={Boolean(seriesId)}
                required={!seriesId}
                onChange={(event) => setTitle(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${fieldId}-channel`}>Channel</Label>
              <select
                id={`${fieldId}-channel`}
                value={channelId}
                onChange={(event) => setChannelId(event.target.value)}
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              >
                <option value="">All channels</option>
                {!validChannel && (
                  <option value={channelId} disabled>
                    Unavailable channel
                  </option>
                )}
                {channels.map((channel) => (
                  <option key={channel.id} value={channel.id}>
                    {channelLabel(channel)}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <div className="flex items-center gap-3">
            <Switch id={`${fieldId}-new`} checked={newOnly} onCheckedChange={setNewOnly} />
            <Label htmlFor={`${fieldId}-new`}>Only programs marked new in the guide</Label>
          </div>
          <Button
            type="submit"
            disabled={unavailable || (!seriesId && !title.trim()) || !validChannel}
          >
            {create.isPending ? "Creating…" : "Create rule"}
          </Button>
        </fieldset>
      </form>
      {rules.isPending && (
        <p role="status" className="text-muted-foreground text-sm">
          Loading recording rules…
        </p>
      )}
      {rules.isSuccess && rules.data.length === 0 && (
        <p className="text-muted-foreground text-sm">No series recording rules yet.</p>
      )}
      {!rules.isError && rules.data && rules.data.length > 0 && (
        <ul className="divide-border divide-y border-y" aria-label="Recording rules">
          {rules.data.map((rule) => {
            const channel = channels.find((item) => item.id === rule.channel_id);
            return (
              <li key={rule.id} className="flex flex-wrap items-center justify-between gap-4 py-4">
                <div className="min-w-0 space-y-1">
                  <p className="font-medium break-words">{ruleLabel(rule)}</p>
                  <p className="text-muted-foreground text-sm">
                    {rule.channel_id
                      ? channel
                        ? channelLabel(channel)
                        : "Unavailable channel"
                      : "All channels"}
                    {rule.new_only ? " · New programs only" : " · Includes repeats"}
                  </p>
                  <Badge variant={rule.enabled ? "secondary" : "outline"}>
                    {rule.enabled ? "Enabled" : "Disabled"}
                  </Badge>
                </div>
                <Button
                  variant="outline"
                  disabled={unavailable}
                  aria-label={`Remove rule: ${ruleLabel(rule)}`}
                  onClick={() => setConfirmRule(rule)}
                >
                  Remove
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      <ConfirmDialog
        open={confirmRule !== null}
        onOpenChange={(open) => !open && setConfirmRule(null)}
        title={`Remove recording rule: ${confirmRule ? ruleLabel(confirmRule) : ""}`}
        description="This removes the recurring rule. Already scheduled recordings and recorded files are not deleted. Manage those separately under My recordings."
        confirmLabel="Remove rule"
        variant="destructive"
        isPending={unavailable}
        onConfirm={() => void deleteRule()}
      />
    </section>
  );
}
