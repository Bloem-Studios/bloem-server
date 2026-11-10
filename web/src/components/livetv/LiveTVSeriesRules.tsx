import { useId, useState, type FormEvent } from "react";
import { captureProfileRequestContext } from "@/api/client";
import type { LiveTVChannel, LiveTVProgram, LiveTVSeriesRule } from "@/api/bloemTypes";
import { useBloemT, type BloemTranslate } from "@/bloem/i18n";
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
  const bt = useBloemT();
  const { profile } = useAuth();
  const authority = captureProfileRequestContext();
  if (!authority || authority.profileId !== profile?.id) {
    return <p role="alert">{bt("bloem.liveTv.seriesRules.selectProfile")}</p>;
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

function ruleLabel(rule: LiveTVSeriesRule, bt: BloemTranslate) {
  return (
    rule.title_match ||
    (rule.series_id
      ? bt("bloem.liveTv.seriesRules.seriesLabel", { seriesId: rule.series_id })
      : bt("bloem.liveTv.seriesRules.allProgramsOnChannel"))
  );
}

function SeriesRulesContent({ channels, programs = [], selectedProgram }: SeriesRuleProps) {
  const bt = useBloemT();
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
      setNotice(bt("bloem.liveTv.seriesRules.created"));
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : bt("bloem.liveTv.seriesRules.createFailed"),
      );
    }
  }

  async function deleteRule() {
    if (!confirmRule || unavailable) return;
    setError(null);
    setNotice(null);
    try {
      await remove.mutateAsync(confirmRule.id);
      setNotice(bt("bloem.liveTv.seriesRules.removed"));
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : bt("bloem.liveTv.seriesRules.removeFailed"),
      );
    } finally {
      setConfirmRule(null);
    }
  }

  return (
    <section className="space-y-6" aria-labelledby={`${fieldId}-heading`}>
      <div className="space-y-2">
        <h2 id={`${fieldId}-heading`} className="text-xl font-semibold">
          {bt("bloem.liveTv.seriesRules.heading")}
        </h2>
        <p className="text-muted-foreground max-w-2xl text-sm">
          {bt("bloem.liveTv.seriesRules.intro")}
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
            {bt("bloem.liveTv.seriesRules.reload")}
          </Button>
        </div>
      )}
      <form
        onSubmit={(event) => void submit(event)}
        aria-label={bt("bloem.liveTv.seriesRules.createFormLabel")}
      >
        <fieldset disabled={busy} className="border-border space-y-4 rounded-xl border p-4">
          <legend className="px-2 font-medium">
            {bt("bloem.liveTv.seriesRules.createLegend")}
          </legend>
          {series.size > 0 && (
            <div className="space-y-2">
              <Label htmlFor={`${fieldId}-series`}>
                {bt("bloem.liveTv.seriesRules.matchSeries")}
              </Label>
              <select
                id={`${fieldId}-series`}
                value={seriesId}
                onChange={(event) => setSeriesId(event.target.value)}
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              >
                <option value="">{bt("bloem.liveTv.seriesRules.useTitleMatching")}</option>
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
              <Label htmlFor={`${fieldId}-title`}>
                {bt("bloem.liveTv.seriesRules.titleContains")}
              </Label>
              <Input
                id={`${fieldId}-title`}
                value={title}
                disabled={Boolean(seriesId)}
                required={!seriesId}
                onChange={(event) => setTitle(event.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${fieldId}-channel`}>{bt("bloem.liveTv.seriesRules.channel")}</Label>
              <select
                id={`${fieldId}-channel`}
                value={channelId}
                onChange={(event) => setChannelId(event.target.value)}
                className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
              >
                <option value="">{bt("bloem.liveTv.seriesRules.allChannels")}</option>
                {!validChannel && (
                  <option value={channelId} disabled>
                    {bt("bloem.liveTv.seriesRules.unavailableChannel")}
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
            <Label htmlFor={`${fieldId}-new`}>{bt("bloem.liveTv.seriesRules.newOnly")}</Label>
          </div>
          <Button
            type="submit"
            disabled={unavailable || (!seriesId && !title.trim()) || !validChannel}
          >
            {create.isPending
              ? bt("bloem.liveTv.seriesRules.creating")
              : bt("bloem.liveTv.seriesRules.createRule")}
          </Button>
        </fieldset>
      </form>
      {rules.isPending && (
        <p role="status" className="text-muted-foreground text-sm">
          {bt("bloem.liveTv.seriesRules.loading")}
        </p>
      )}
      {rules.isSuccess && rules.data.length === 0 && (
        <p className="text-muted-foreground text-sm">{bt("bloem.liveTv.seriesRules.empty")}</p>
      )}
      {!rules.isError && rules.data && rules.data.length > 0 && (
        <ul
          className="divide-border divide-y border-y"
          aria-label={bt("bloem.liveTv.seriesRules.listLabel")}
        >
          {rules.data.map((rule) => {
            const channel = channels.find((item) => item.id === rule.channel_id);
            const channelText = rule.channel_id
              ? channel
                ? channelLabel(channel)
                : bt("bloem.liveTv.seriesRules.unavailableChannel")
              : bt("bloem.liveTv.seriesRules.allChannels");
            return (
              <li key={rule.id} className="flex flex-wrap items-center justify-between gap-4 py-4">
                <div className="min-w-0 space-y-1">
                  <p className="font-medium break-words">{ruleLabel(rule, bt)}</p>
                  <p className="text-muted-foreground text-sm">
                    {rule.new_only
                      ? bt("bloem.liveTv.seriesRules.summaryNewOnly", { channel: channelText })
                      : bt("bloem.liveTv.seriesRules.summaryRepeats", { channel: channelText })}
                  </p>
                  <Badge variant={rule.enabled ? "secondary" : "outline"}>
                    {rule.enabled
                      ? bt("bloem.liveTv.seriesRules.enabled")
                      : bt("bloem.liveTv.seriesRules.disabled")}
                  </Badge>
                </div>
                <Button
                  variant="outline"
                  disabled={unavailable}
                  aria-label={bt("bloem.liveTv.seriesRules.removeRuleLabel", {
                    rule: ruleLabel(rule, bt),
                  })}
                  onClick={() => setConfirmRule(rule)}
                >
                  {bt("bloem.liveTv.seriesRules.remove")}
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      <ConfirmDialog
        open={confirmRule !== null}
        onOpenChange={(open) => !open && setConfirmRule(null)}
        title={bt("bloem.liveTv.seriesRules.confirmTitle", {
          rule: confirmRule ? ruleLabel(confirmRule, bt) : "",
        })}
        description={bt("bloem.liveTv.seriesRules.confirmDescription")}
        confirmLabel={bt("bloem.liveTv.seriesRules.confirmRemove")}
        variant="destructive"
        isPending={unavailable}
        onConfirm={() => void deleteRule()}
      />
    </section>
  );
}
