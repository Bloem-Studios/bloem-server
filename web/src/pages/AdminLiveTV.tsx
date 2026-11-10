import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { Plus, Radar, RefreshCw, Trash2, X } from "lucide-react";
import type { LiveTVDiscoveredTuner } from "@/api/bloemTypes";
import { useBloemT, type BloemTranslate } from "@/bloem/i18n";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  useAddLiveTVTuner,
  useCancelLiveTVRecording,
  useCreateLiveTVGuideSource,
  useDeleteLiveTVGuideSource,
  useDeleteLiveTVTuner,
  useDiscoverLiveTVTuners,
  useLiveTVChannels,
  useLiveTVGuideSources,
  useLiveTVRecordings,
  useLiveTVTuners,
  useLookupSchedulesDirectLineups,
  useLookupXMLSyncLineups,
  usePatchLiveTVChannel,
  useScanLiveTVTuner,
  useSyncLiveTVGuideSource,
  useUpdateLiveTVGuideSource,
} from "@/hooks/queries/useLiveTV";
import type { SchedulesDirectLineupOption, XMLSyncLineupOption } from "@/api/bloemTypes";
import { formatLocale } from "@/lib/appLocale";
import { LiveTVTranscodingTab } from "@/pages/livetv/LiveTVTranscodingTab";
import { XtreamProviderForm } from "@/components/livetv/XtreamProviderForm";
import { XtreamGuideSourceForm } from "@/components/livetv/XtreamGuideSourceForm";
import { XtreamRemoveButton } from "@/components/livetv/XtreamRemoveButton";

const LIVETV_TABS = ["tuners", "channels", "guide", "recordings", "transcoding"] as const;
type LiveTVTab = (typeof LIVETV_TABS)[number];

// Product names, shown as they are in every language.
const DISPATCHARR = "Dispatcharr";
const HDHOMERUN = "HDHomeRun";
const SCHEDULES_DIRECT = "Schedules Direct";

// Example values in empty fields: addresses, codes and IDs, not words.
const TUNER_URL_PLACEHOLDER = "http://192.168.1.50";
const STATION_ID_PLACEHOLDER = "12345";
const COUNTRY_PLACEHOLDER = "USA";
const POSTAL_CODE_PLACEHOLDER = "12345";
const XML_LINEUP_PLACEHOLDER = "USA-lineupId-DEFAULT";
const XML_HEADEND_PLACEHOLDER = "lineupId";
const SD_LINEUP_PLACEHOLDER = "USA-OTA-12345";

// Default names for a new guide source. They are saved on the server as the
// source's name, so they stay English: data, not interface text.
const SD_DEFAULT_NAME = "Schedules Direct";
const XML_DEFAULT_NAME = "Free listings";

function normalizeTab(value: string | null): LiveTVTab {
  return LIVETV_TABS.includes(value as LiveTVTab) ? (value as LiveTVTab) : "tuners";
}

function readable(value: string): string {
  const text = value.replace(/_/g, " ").trim();
  return text ? text[0]!.toUpperCase() + text.slice(1) : value;
}

/** A tuner's, guide source's or recording's status; one the page doesn't know shows as sent. */
function statusLabel(bt: BloemTranslate, status: string): string {
  switch (status) {
    case "pending":
      return bt("bloem.admin.liveTv.status.pending");
    case "discovered":
      return bt("bloem.admin.liveTv.status.discovered");
    case "ready":
      return bt("bloem.admin.liveTv.status.ready");
    case "error":
      return bt("bloem.admin.liveTv.status.error");
    case "idle":
      return bt("bloem.admin.liveTv.status.idle");
    case "syncing":
      return bt("bloem.admin.liveTv.status.syncing");
    case "scheduled":
      return bt("bloem.admin.liveTv.status.scheduled");
    case "recording":
      return bt("bloem.admin.liveTv.status.recording");
    case "completed":
      return bt("bloem.admin.liveTv.status.completed");
    case "failed":
      return bt("bloem.admin.liveTv.status.failed");
    case "cancelled":
      return bt("bloem.admin.liveTv.status.cancelled");
    default:
      return readable(status);
  }
}

function kindLabel(bt: BloemTranslate, kind: string): string {
  if (kind === "dispatcharr") return DISPATCHARR;
  if (kind === "hdhomerun") return HDHOMERUN;
  return kind || bt("bloem.admin.liveTv.tuners.found.tuner");
}

function TunersTab() {
  const bt = useBloemT();
  const tuners = useLiveTVTuners();
  const addTuner = useAddLiveTVTuner();
  const discoverTuners = useDiscoverLiveTVTuners();
  const scanTuner = useScanLiveTVTuner();
  const deleteTuner = useDeleteLiveTVTuner();
  const [tunerURL, setTunerURL] = useState("");
  const [candidates, setCandidates] = useState<LiveTVDiscoveredTuner[]>([]);
  const [discoverNotes, setDiscoverNotes] = useState<string[]>([]);
  const [searched, setSearched] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);

  function submit() {
    addTuner.mutate(
      { url: tunerURL.trim() },
      {
        onSuccess: () => {
          setTunerURL("");
        },
      },
    );
  }

  function runDiscovery() {
    const probe = tunerURL.trim();
    discoverTuners.mutate(
      {
        timeout_ms: 2500,
        include_udp: true,
        probe_urls: probe ? [probe] : undefined,
      },
      {
        onSuccess: (data) => {
          setCandidates(data.candidates ?? []);
          setDiscoverNotes(data.notes ?? []);
          setSearched(true);
        },
      },
    );
  }

  function addCandidate(c: LiveTVDiscoveredTuner) {
    const url = c.base_url || c.discover_url;
    if (!url) return;
    addTuner.mutate(
      { url },
      {
        onSuccess: () => {
          setCandidates((prev) =>
            prev.map((row) =>
              row.device_id === c.device_id && row.base_url === c.base_url
                ? { ...row, already_added: true }
                : row,
            ),
          );
        },
      },
    );
  }

  return (
    <div className="space-y-8">
      <section className="space-y-3" aria-labelledby="your-tuners">
        <h2 id="your-tuners" className="text-base font-medium">
          {bt("bloem.admin.liveTv.tuners.heading")}
        </h2>
        {tuners.isLoading ? (
          <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.loading")}</p>
        ) : (tuners.data?.length ?? 0) === 0 ? (
          <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.tuners.none")}</p>
        ) : (
          <ul className="divide-border divide-y border-y">
            {tuners.data?.map((tuner) => (
              <li key={tuner.id} className="flex flex-wrap items-center justify-between gap-3 py-4">
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{tuner.model || tuner.device_id}</span>
                    <Badge variant="secondary">{statusLabel(bt, tuner.status)}</Badge>
                    <span className="text-muted-foreground text-sm tabular-nums">
                      {bt("bloem.admin.liveTv.tuners.channelCount", {
                        count: tuner.channel_count,
                        shown: tuner.channel_count,
                      })}
                    </span>
                  </div>
                  <p className="text-muted-foreground truncate font-mono text-xs">
                    {tuner.base_url || tuner.device_id}
                  </p>
                  {tuner.last_error ? (
                    <p className="text-destructive text-xs">
                      {bt("bloem.admin.liveTv.lastProblem", { error: tuner.last_error })}
                    </p>
                  ) : null}
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={scanTuner.isPending}
                    onClick={() => scanTuner.mutate(tuner.id)}
                  >
                    <RefreshCw />
                    {bt("bloem.admin.liveTv.tuners.findChannelsAgain")}
                  </Button>
                  {tuner.type === "xtream" ? (
                    <XtreamRemoveButton
                      id={tuner.id}
                      pending={deleteTuner.isPending}
                      onRemove={(id) => deleteTuner.mutate(id)}
                    />
                  ) : (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={deleteTuner.isPending}
                      onClick={() => setRemoving(tuner.id)}
                    >
                      <Trash2 />
                      {bt("bloem.admin.liveTv.tuners.remove")}
                    </Button>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
        <ConfirmDialog
          open={removing !== null}
          onOpenChange={(open) => !open && setRemoving(null)}
          title={bt("bloem.admin.liveTv.tuners.removeConfirm.title")}
          description={bt("bloem.admin.liveTv.tuners.removeConfirm.description")}
          confirmLabel={bt("bloem.admin.liveTv.tuners.removeConfirm.confirm")}
          variant="destructive"
          isPending={deleteTuner.isPending}
          onConfirm={() => {
            if (removing) deleteTuner.mutate(removing);
            setRemoving(null);
          }}
        />
      </section>

      <section className="max-w-2xl space-y-4" aria-labelledby="add-tuner">
        <div>
          <h2 id="add-tuner" className="text-base font-medium">
            {bt("bloem.admin.liveTv.tuners.add.heading")}
          </h2>
          <p className="text-muted-foreground mt-1 text-sm">
            {bt("bloem.admin.liveTv.tuners.add.intro")}
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="tuner-url">{bt("bloem.admin.liveTv.tuners.add.address")}</Label>
          <Input
            id="tuner-url"
            placeholder={TUNER_URL_PLACEHOLDER}
            value={tunerURL}
            onChange={(e) => setTunerURL(e.target.value)}
          />
          <p className="text-muted-foreground text-xs">
            {bt("bloem.admin.liveTv.tuners.add.addressHelp")}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button onClick={submit} disabled={addTuner.isPending || !tunerURL.trim()}>
            <Plus />
            {addTuner.isPending
              ? bt("bloem.admin.liveTv.tuners.add.adding")
              : bt("bloem.admin.liveTv.tuners.add.addTuner")}
          </Button>
          <Button variant="outline" onClick={runDiscovery} disabled={discoverTuners.isPending}>
            <Radar />
            {discoverTuners.isPending
              ? bt("bloem.admin.liveTv.tuners.add.searching")
              : bt("bloem.admin.liveTv.tuners.add.searchNetwork")}
          </Button>
        </div>
        {searched && candidates.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.liveTv.tuners.add.noneFound")}
          </p>
        ) : null}
        {candidates.length > 0 ? (
          <ul
            className="divide-border divide-y border-y"
            aria-label={bt("bloem.admin.liveTv.tuners.found.label")}
          >
            {candidates.map((c) => (
              <li
                key={`${c.device_id}-${c.base_url || c.discover_url}`}
                className="flex flex-wrap items-center justify-between gap-3 py-3"
              >
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">
                      {c.friendly_name ||
                        c.model ||
                        c.device_id ||
                        bt("bloem.admin.liveTv.tuners.found.tuner")}
                    </span>
                    <span className="text-muted-foreground text-sm">
                      {c.tuner_count > 0
                        ? bt("bloem.admin.liveTv.tuners.found.kindAndTunerCount", {
                            kind: kindLabel(bt, c.kind),
                            count: c.tuner_count,
                          })
                        : kindLabel(bt, c.kind)}
                    </span>
                  </div>
                  <p className="text-muted-foreground truncate font-mono text-xs">
                    {c.base_url || c.discover_url}
                  </p>
                </div>
                {c.already_added ? (
                  <span className="text-muted-foreground text-sm">
                    {bt("bloem.admin.liveTv.tuners.found.added")}
                  </span>
                ) : (
                  <Button
                    size="sm"
                    disabled={addTuner.isPending || !(c.base_url || c.discover_url)}
                    onClick={() => addCandidate(c)}
                  >
                    <Plus />
                    {bt("bloem.admin.liveTv.tuners.found.add")}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        ) : null}
        {discoverNotes.length > 0 ? (
          <ul className="text-muted-foreground list-disc space-y-1 ps-5 text-xs">
            {discoverNotes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        ) : null}
        <details className="text-sm">
          <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
            {bt("bloem.admin.liveTv.tuners.add.searchHelp.summary")}
          </summary>
          <p className="text-muted-foreground mt-2">
            {bt("bloem.admin.liveTv.tuners.add.searchHelp.body")}
          </p>
        </details>
      </section>

      <div className="max-w-2xl">
        <XtreamProviderForm />
      </div>
    </div>
  );
}

function ChannelsTab() {
  const bt = useBloemT();
  const channels = useLiveTVChannels();
  const patchChannel = usePatchLiveTVChannel();
  const [editingStation, setEditingStation] = useState<string | null>(null);
  const [stationDrafts, setStationDrafts] = useState<Record<string, string>>({});

  const sorted = useMemo(
    () =>
      [...(channels.data ?? [])].sort((a, b) =>
        (a.number_override || a.number).localeCompare(b.number_override || b.number, undefined, {
          numeric: true,
        }),
      ),
    [channels.data],
  );

  const mappedCount = sorted.filter((ch) => Boolean(ch.guide_station_id)).length;

  return (
    <div className="space-y-4">
      <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.channels.intro")}</p>
      {sorted.length > 0 ? (
        <p className="text-muted-foreground text-xs">
          {bt(
            mappedCount < sorted.length
              ? "bloem.admin.liveTv.channels.coverage"
              : "bloem.admin.liveTv.channels.coverageComplete",
            { mapped: mappedCount, total: sorted.length, totalShown: sorted.length },
          )}
        </p>
      ) : null}
      {channels.isLoading ? (
        <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.loading")}</p>
      ) : sorted.length === 0 ? (
        <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.channels.none")}</p>
      ) : (
        <ul className="divide-border divide-y border-y">
          {sorted.map((channel) => {
            const mapped = Boolean(channel.guide_station_id);
            const editing = editingStation === channel.id;
            const stationValue = stationDrafts[channel.id] ?? channel.guide_station_id ?? "";
            return (
              <li key={channel.id} className="grid gap-3 py-4 sm:grid-cols-[1fr_auto]">
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">
                      {bt("bloem.admin.liveTv.channels.channelName", {
                        number: channel.number_override || channel.number,
                        name: channel.callsign || channel.name,
                      })}
                    </span>
                    {channel.hd ? (
                      <Badge variant="secondary">{bt("bloem.admin.liveTv.channels.hd")}</Badge>
                    ) : null}
                    {mapped ? (
                      <Badge
                        variant="outline"
                        title={bt("bloem.admin.liveTv.channels.station", {
                          stationId: channel.guide_station_id,
                        })}
                      >
                        {bt("bloem.admin.liveTv.channels.listingsMatched")}
                      </Badge>
                    ) : (
                      <Badge variant="secondary">
                        {bt("bloem.admin.liveTv.channels.noListings")}
                      </Badge>
                    )}
                  </div>
                  {editing ? (
                    <div className="flex flex-wrap items-end gap-2">
                      <div className="space-y-1">
                        <Label htmlFor={`station-${channel.id}`} className="text-xs">
                          {bt("bloem.admin.liveTv.channels.stationIdLabel")}
                        </Label>
                        <Input
                          id={`station-${channel.id}`}
                          className="w-48"
                          placeholder={STATION_ID_PLACEHOLDER}
                          autoFocus
                          value={stationValue}
                          onChange={(e) =>
                            setStationDrafts((prev) => ({
                              ...prev,
                              [channel.id]: e.target.value,
                            }))
                          }
                          onBlur={() => {
                            if (stationValue === (channel.guide_station_id || "")) {
                              setEditingStation(null);
                              return;
                            }
                            patchChannel.mutate({
                              channelId: channel.id,
                              body: { guide_station_id: stationValue },
                            });
                            setEditingStation(null);
                          }}
                          onKeyDown={(e) => {
                            if (e.key === "Escape") {
                              setStationDrafts((prev) => {
                                const next = { ...prev };
                                delete next[channel.id];
                                return next;
                              });
                              setEditingStation(null);
                            }
                          }}
                        />
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        onClick={() => setEditingStation(null)}
                      >
                        {bt("bloem.admin.liveTv.channels.done")}
                      </Button>
                    </div>
                  ) : (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="text-muted-foreground h-auto px-0"
                      onClick={() => setEditingStation(channel.id)}
                    >
                      {mapped
                        ? bt("bloem.admin.liveTv.channels.fixMatch")
                        : bt("bloem.admin.liveTv.channels.matchByHand")}
                    </Button>
                  )}
                </div>
                <div className="flex items-center gap-2 self-start sm:self-center">
                  <Label
                    htmlFor={`enabled-${channel.id}`}
                    className="text-muted-foreground text-sm"
                  >
                    {bt("bloem.admin.liveTv.channels.showInLiveTv")}
                  </Label>
                  <Switch
                    id={`enabled-${channel.id}`}
                    checked={channel.enabled}
                    disabled={patchChannel.isPending}
                    onCheckedChange={(checked) =>
                      patchChannel.mutate({
                        channelId: channel.id,
                        body: { enabled: checked },
                      })
                    }
                  />
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function GuideTab() {
  const bt = useBloemT();
  const sources = useLiveTVGuideSources();
  const createSource = useCreateLiveTVGuideSource();
  const updateSource = useUpdateLiveTVGuideSource();
  const deleteSource = useDeleteLiveTVGuideSource();
  const syncSource = useSyncLiveTVGuideSource();
  const lookupSDLineups = useLookupSchedulesDirectLineups();
  const lookupXMLLineups = useLookupXMLSyncLineups();

  const [sdDisplayName, setSDDisplayName] = useState(SD_DEFAULT_NAME);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [sdCountry, setSDCountry] = useState("USA");
  const [sdPostalCode, setSDPostalCode] = useState("");
  const [sdLineup, setSDLineup] = useState("");
  const [sdLineups, setSDLineups] = useState<SchedulesDirectLineupOption[]>([]);

  const [xmlDisplayName, setXMLDisplayName] = useState(XML_DEFAULT_NAME);
  const [kind, setKind] = useState<"xml" | "sd" | "xtream">("xml");
  const [removing, setRemoving] = useState<string | null>(null);
  const [xmlCountry, setXMLCountry] = useState("USA");
  const [xmlPostalCode, setXMLPostalCode] = useState("");
  const [xmlLineup, setXMLLineup] = useState("");
  const [xmlHeadend, setXMLHeadend] = useState("");
  const [xmlDevice, setXMLDevice] = useState("-");
  const [xmlLineups, setXMLLineups] = useState<XMLSyncLineupOption[]>([]);

  const enabledCount = sources.data?.filter((s) => s.enabled).length ?? 0;

  function findSDLineups() {
    lookupSDLineups.mutate(
      {
        username: username.trim(),
        password,
        country: sdCountry.trim() || "USA",
        postalcode: sdPostalCode.trim(),
      },
      {
        onSuccess: (found) => {
          setSDLineups(found);
          const preferred =
            found.find((item) => /antenna|ota/i.test(`${item.transport} ${item.name}`)) ?? found[0];
          setSDLineup(preferred?.lineup ?? "");
        },
      },
    );
  }

  function findXMLLineups() {
    lookupXMLLineups.mutate(
      {
        country: xmlCountry.trim() || "USA",
        postalcode: xmlPostalCode.trim(),
      },
      {
        onSuccess: (found) => {
          setXMLLineups(found);
          const preferred =
            found.find((item) =>
              /ota|antenna|over the air/i.test(`${item.transport} ${item.name}`),
            ) ?? found[0];
          setXMLLineup(preferred?.lineup ?? "");
          setXMLHeadend(preferred?.headend ?? "");
          setXMLDevice(preferred?.device || "-");
        },
      },
    );
  }

  function addSchedulesDirect() {
    createSource.mutate(
      {
        type: "schedules_direct",
        enabled: true,
        display_name: sdDisplayName.trim() || SD_DEFAULT_NAME,
        priority: 100,
        config: {
          username: username.trim(),
          password,
          country: sdCountry.trim() || "USA",
          postalcode: sdPostalCode.trim(),
          lineup: sdLineup.trim(),
        },
      },
      {
        onSuccess: () => {
          setPassword("");
          setSDLineups([]);
        },
      },
    );
  }

  function addXMLSync() {
    createSource.mutate(
      {
        type: "xml_sync",
        enabled: true,
        display_name: xmlDisplayName.trim() || XML_DEFAULT_NAME,
        priority: 100,
        config: {
          country: xmlCountry.trim() || "USA",
          postalcode: xmlPostalCode.trim(),
          lineup: xmlLineup.trim(),
          headend: xmlHeadend.trim(),
          device: xmlDevice.trim() || "-",
        },
      },
      {
        onSuccess: () => {
          setXMLLineups([]);
        },
      },
    );
  }

  const canAddSD = Boolean(username.trim() && password && sdLineup.trim()) && enabledCount < 3;
  const canAddXML = Boolean(xmlPostalCode.trim() && xmlLineup.trim()) && enabledCount < 3;
  const atLimit = enabledCount >= 3;

  return (
    <div className="space-y-8">
      <section className="space-y-3" aria-labelledby="guide-sources">
        <div>
          <h2 id="guide-sources" className="text-base font-medium">
            {bt("bloem.admin.liveTv.guide.sources.heading")}
          </h2>
          <p className="text-muted-foreground mt-1 max-w-2xl text-sm">
            {bt("bloem.admin.liveTv.guide.sources.intro")}
          </p>
        </div>
        {sources.isLoading ? (
          <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.loading")}</p>
        ) : (sources.data?.length ?? 0) === 0 ? (
          <p className="text-muted-foreground text-sm">
            {bt("bloem.admin.liveTv.guide.sources.none")}
          </p>
        ) : (
          <ul className="divide-border divide-y border-y">
            {sources.data?.map((source) => {
              const syncState = source.last_sync_at
                ? bt("bloem.admin.liveTv.guide.sources.lastSynced", {
                    time: new Date(source.last_sync_at).toLocaleString(formatLocale()),
                  })
                : bt("bloem.admin.liveTv.guide.sources.notSynced");
              return (
                <li
                  key={source.id}
                  className="flex flex-wrap items-center justify-between gap-3 py-4"
                >
                  <div className="min-w-0 space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">
                        {source.display_name || sourceTypeLabel(bt, source.type)}
                      </span>
                      <span className="text-muted-foreground text-sm">
                        {sourceTypeLabel(bt, source.type)}
                      </span>
                      <Badge variant="outline">{statusLabel(bt, source.status)}</Badge>
                    </div>
                    <p className="text-muted-foreground truncate text-xs">
                      {source.config?.postalcode
                        ? bt("bloem.admin.liveTv.guide.sources.withPostalCode", {
                            postalCode: source.config.postalcode,
                            syncState,
                          })
                        : syncState}
                    </p>
                    {source.last_error ? (
                      <p className="text-destructive text-xs">
                        {bt("bloem.admin.liveTv.lastProblem", { error: source.last_error })}
                      </p>
                    ) : null}
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <div className="flex items-center gap-2">
                      <Label
                        htmlFor={`source-enabled-${source.id}`}
                        className="text-muted-foreground text-sm"
                      >
                        {bt("bloem.admin.liveTv.guide.sources.on")}
                      </Label>
                      <Switch
                        id={`source-enabled-${source.id}`}
                        checked={source.enabled}
                        disabled={updateSource.isPending || (!source.enabled && atLimit)}
                        onCheckedChange={(checked) =>
                          updateSource.mutate({
                            id: source.id,
                            body: { enabled: checked },
                          })
                        }
                      />
                    </div>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={syncSource.isPending}
                      onClick={() => syncSource.mutate(source.id)}
                    >
                      <RefreshCw />
                      {bt("bloem.admin.liveTv.guide.sources.syncNow")}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={deleteSource.isPending}
                      onClick={() => setRemoving(source.id)}
                    >
                      <Trash2 />
                      {bt("bloem.admin.liveTv.guide.sources.remove")}
                    </Button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
        <ConfirmDialog
          open={removing !== null}
          onOpenChange={(open) => !open && setRemoving(null)}
          title={bt("bloem.admin.liveTv.guide.removeConfirm.title")}
          description={bt("bloem.admin.liveTv.guide.removeConfirm.description")}
          confirmLabel={bt("bloem.admin.liveTv.guide.removeConfirm.confirm")}
          variant="destructive"
          isPending={deleteSource.isPending}
          onConfirm={() => {
            if (removing) deleteSource.mutate(removing);
            setRemoving(null);
          }}
        />
      </section>

      <section className="max-w-xl space-y-4" aria-labelledby="add-guide-source">
        <h2 id="add-guide-source" className="text-base font-medium">
          {bt("bloem.admin.liveTv.guide.add.heading")}
        </h2>
        {atLimit ? (
          <p className="border-border bg-muted/40 rounded-lg border p-3 text-sm">
            {bt("bloem.admin.liveTv.guide.add.atLimit")}
          </p>
        ) : null}
        <fieldset
          className="grid gap-2"
          role="radiogroup"
          aria-label={bt("bloem.admin.liveTv.guide.add.kind")}
        >
          {(
            [
              [
                "xml",
                bt("bloem.admin.liveTv.guide.kind.xml.label"),
                bt("bloem.admin.liveTv.guide.kind.xml.hint"),
              ],
              ["sd", SCHEDULES_DIRECT, bt("bloem.admin.liveTv.guide.kind.sd.hint")],
              [
                "xtream",
                bt("bloem.admin.liveTv.guide.kind.xtream.label"),
                bt("bloem.admin.liveTv.guide.kind.xtream.hint"),
              ],
            ] as const
          ).map(([value, label, hint]) => (
            <label
              key={value}
              className="border-border has-[:checked]:border-primary has-[:checked]:bg-primary/5 hover:bg-muted/40 flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors"
            >
              <input
                type="radio"
                name="guide-source-kind"
                className="mt-1"
                aria-label={label}
                checked={kind === value}
                onChange={() => setKind(value)}
              />
              <span>
                <span className="block text-sm font-medium">{label}</span>
                <span className="text-muted-foreground block text-sm">{hint}</span>
              </span>
            </label>
          ))}
        </fieldset>

        {kind === "xtream" ? <XtreamGuideSourceForm /> : null}

        {kind === "xml" ? (
          <div className="space-y-4">
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.liveTv.guide.xml.intro")}
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="xml-country">{bt("bloem.admin.liveTv.guide.country")}</Label>
                <Input
                  id="xml-country"
                  value={xmlCountry}
                  onChange={(e) => setXMLCountry(e.target.value)}
                  placeholder={COUNTRY_PLACEHOLDER}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="xml-postal">{bt("bloem.admin.liveTv.guide.postalCode")}</Label>
                <Input
                  id="xml-postal"
                  value={xmlPostalCode}
                  onChange={(e) => setXMLPostalCode(e.target.value)}
                  placeholder={POSTAL_CODE_PLACEHOLDER}
                />
              </div>
            </div>
            <Button
              type="button"
              variant="outline"
              onClick={findXMLLineups}
              disabled={lookupXMLLineups.isPending || !xmlPostalCode.trim()}
            >
              <Radar />
              {lookupXMLLineups.isPending
                ? bt("bloem.admin.liveTv.guide.lookingUp")
                : bt("bloem.admin.liveTv.guide.findLineups")}
            </Button>
            {xmlLineups.length > 0 ? (
              <div className="space-y-1.5">
                <Label htmlFor="xml-lineup">{bt("bloem.admin.liveTv.guide.lineup")}</Label>
                <select
                  id="xml-lineup"
                  className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                  value={xmlLineup}
                  onChange={(e) => {
                    const next = e.target.value;
                    setXMLLineup(next);
                    const match = xmlLineups.find((item) => item.lineup === next);
                    if (match) {
                      setXMLHeadend(match.headend);
                      setXMLDevice(match.device || "-");
                    }
                  }}
                >
                  {xmlLineups.map((item) => (
                    <option key={`${item.lineup}:${item.headend}`} value={item.lineup}>
                      {item.location
                        ? bt("bloem.admin.liveTv.guide.lineupOptionWithLocation", {
                            name: item.name,
                            location: item.location,
                            transport: item.transport,
                          })
                        : bt("bloem.admin.liveTv.guide.lineupOption", {
                            name: item.name,
                            transport: item.transport,
                          })}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <details className="text-sm">
                <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
                  {bt("bloem.admin.liveTv.guide.enterLineupId")}
                </summary>
                <div className="mt-3 grid gap-4 sm:grid-cols-2">
                  <div className="space-y-1.5">
                    <Label htmlFor="xml-lineup-manual">
                      {bt("bloem.admin.liveTv.guide.lineupId")}
                    </Label>
                    <Input
                      id="xml-lineup-manual"
                      value={xmlLineup}
                      onChange={(e) => setXMLLineup(e.target.value)}
                      placeholder={XML_LINEUP_PLACEHOLDER}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="xml-headend-manual">
                      {bt("bloem.admin.liveTv.guide.headendId")}
                    </Label>
                    <Input
                      id="xml-headend-manual"
                      value={xmlHeadend}
                      onChange={(e) => setXMLHeadend(e.target.value)}
                      placeholder={XML_HEADEND_PLACEHOLDER}
                    />
                  </div>
                </div>
              </details>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="xml-guide-name">{bt("bloem.admin.liveTv.guide.name")}</Label>
              <Input
                id="xml-guide-name"
                value={xmlDisplayName}
                onChange={(e) => setXMLDisplayName(e.target.value)}
              />
            </div>
            <Button onClick={addXMLSync} disabled={createSource.isPending || !canAddXML}>
              <Plus />
              {createSource.isPending
                ? bt("bloem.admin.liveTv.guide.adding")
                : bt("bloem.admin.liveTv.guide.xml.add")}
            </Button>
          </div>
        ) : null}

        {kind === "sd" ? (
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault();
              if (!createSource.isPending && canAddSD) {
                addSchedulesDirect();
              }
            }}
          >
            <p className="text-muted-foreground text-sm">
              {bt("bloem.admin.liveTv.guide.sd.intro", {
                link: (chunks) => (
                  <a
                    href="https://www.schedulesdirect.org/"
                    target="_blank"
                    rel="noreferrer"
                    className="underline underline-offset-2"
                  >
                    {chunks}
                  </a>
                ),
              })}
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="sd-username">{bt("bloem.admin.liveTv.guide.sd.username")}</Label>
                <Input
                  id="sd-username"
                  autoComplete="username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sd-password">{bt("bloem.admin.liveTv.guide.sd.password")}</Label>
                <Input
                  id="sd-password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </div>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="sd-country">{bt("bloem.admin.liveTv.guide.country")}</Label>
                <Input
                  id="sd-country"
                  value={sdCountry}
                  onChange={(e) => setSDCountry(e.target.value)}
                  placeholder={COUNTRY_PLACEHOLDER}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sd-postal">{bt("bloem.admin.liveTv.guide.postalCode")}</Label>
                <Input
                  id="sd-postal"
                  value={sdPostalCode}
                  onChange={(e) => setSDPostalCode(e.target.value)}
                  placeholder={POSTAL_CODE_PLACEHOLDER}
                />
              </div>
            </div>
            <Button
              type="button"
              variant="outline"
              onClick={findSDLineups}
              disabled={
                lookupSDLineups.isPending || !username.trim() || !password || !sdPostalCode.trim()
              }
            >
              <Radar />
              {lookupSDLineups.isPending
                ? bt("bloem.admin.liveTv.guide.lookingUp")
                : bt("bloem.admin.liveTv.guide.findLineups")}
            </Button>
            {sdLineups.length > 0 ? (
              <div className="space-y-1.5">
                <Label htmlFor="sd-lineup">{bt("bloem.admin.liveTv.guide.lineup")}</Label>
                <select
                  id="sd-lineup"
                  className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                  value={sdLineup}
                  onChange={(e) => setSDLineup(e.target.value)}
                >
                  {sdLineups.map((item) => (
                    <option key={item.lineup} value={item.lineup}>
                      {bt("bloem.admin.liveTv.guide.lineupOption", {
                        name: item.name,
                        transport: item.transport,
                      })}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <details className="text-sm">
                <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
                  {bt("bloem.admin.liveTv.guide.enterLineupId")}
                </summary>
                <div className="mt-3 space-y-1.5">
                  <Label htmlFor="sd-lineup-manual">
                    {bt("bloem.admin.liveTv.guide.lineupId")}
                  </Label>
                  <Input
                    id="sd-lineup-manual"
                    value={sdLineup}
                    onChange={(e) => setSDLineup(e.target.value)}
                    placeholder={SD_LINEUP_PLACEHOLDER}
                  />
                </div>
              </details>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="guide-name">{bt("bloem.admin.liveTv.guide.name")}</Label>
              <Input
                id="guide-name"
                value={sdDisplayName}
                onChange={(e) => setSDDisplayName(e.target.value)}
              />
            </div>
            <Button type="submit" disabled={createSource.isPending || !canAddSD}>
              <Plus />
              {createSource.isPending
                ? bt("bloem.admin.liveTv.guide.adding")
                : bt("bloem.admin.liveTv.guide.sd.add")}
            </Button>
          </form>
        ) : null}
      </section>
    </div>
  );
}

function sourceTypeLabel(bt: BloemTranslate, type: string): string {
  if (type === "xml_sync") return bt("bloem.admin.liveTv.guide.kind.xml.label");
  if (type === "schedules_direct") return SCHEDULES_DIRECT;
  if (type === "xtream") return bt("bloem.admin.liveTv.guide.kind.xtream.label");
  return readable(type);
}

function RecordingsTab() {
  const bt = useBloemT();
  const recordings = useLiveTVRecordings();
  const cancelRecording = useCancelLiveTVRecording();

  if (recordings.isLoading) {
    return <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.loading")}</p>;
  }
  if ((recordings.data?.length ?? 0) === 0) {
    return (
      <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.recordings.none")}</p>
    );
  }

  return (
    <ul className="divide-border divide-y border-y">
      {recordings.data?.map((rec) => {
        const start = new Date(rec.start).toLocaleString(formatLocale());
        const stop = new Date(rec.stop).toLocaleString(formatLocale());
        return (
          <li key={rec.id} className="flex flex-wrap items-center justify-between gap-3 py-4">
            <div className="min-w-0 space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">
                  {rec.title || bt("bloem.admin.liveTv.recordings.untitled")}
                </span>
                <Badge variant="secondary">{statusLabel(bt, rec.status)}</Badge>
              </div>
              <p className="text-muted-foreground text-xs">
                {rec.last_error
                  ? bt("bloem.admin.liveTv.recordings.timeWithProblem", {
                      start,
                      stop,
                      error: rec.last_error,
                    })
                  : bt("bloem.admin.liveTv.recordings.time", { start, stop })}
              </p>
            </div>
            {rec.status === "scheduled" ? (
              <Button
                variant="outline"
                size="sm"
                disabled={cancelRecording.isPending}
                onClick={() => cancelRecording.mutate(rec.id)}
              >
                <X />
                {bt("bloem.admin.liveTv.recordings.cancel")}
              </Button>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

export default function AdminLiveTV() {
  const bt = useBloemT();
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab = normalizeTab(searchParams.get("tab"));
  const tuners = useLiveTVTuners();
  const channels = useLiveTVChannels();
  const sources = useLiveTVGuideSources();

  function setActiveTab(value: string) {
    const next = new URLSearchParams(searchParams);
    if (value === "tuners") {
      next.delete("tab");
    } else {
      next.set("tab", value);
    }
    setSearchParams(next, { replace: true });
  }

  return (
    <div className="space-y-6">
      <div className="page-header">
        <div className="space-y-2">
          <div className="flex items-center gap-2.5">
            <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">
              {bt("bloem.admin.liveTv.page.title")}
            </h1>
          </div>
          <p className="text-muted-foreground max-w-2xl text-sm leading-6">
            {bt("bloem.admin.liveTv.page.intro")}
          </p>
          <p className="text-muted-foreground text-sm tabular-nums">
            {bt("bloem.admin.liveTv.page.summary", {
              tuners: tuners.data?.length ?? 0,
              channels: channels.data?.length ?? 0,
              enabledSources: sources.data?.filter((s) => s.enabled).length ?? 0,
            })}
          </p>
        </div>
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab} className="gap-5">
        <TabsList variant="line" className="border-border w-full justify-start border-b">
          <TabsTrigger value="tuners">{bt("bloem.admin.liveTv.tabs.tuners")}</TabsTrigger>
          <TabsTrigger value="channels">{bt("bloem.admin.liveTv.tabs.channels")}</TabsTrigger>
          <TabsTrigger value="guide">{bt("bloem.admin.liveTv.tabs.guide")}</TabsTrigger>
          <TabsTrigger value="recordings">{bt("bloem.admin.liveTv.tabs.recordings")}</TabsTrigger>
          <TabsTrigger value="transcoding">{bt("bloem.admin.liveTv.tabs.conversion")}</TabsTrigger>
        </TabsList>
        <TabsContent value="tuners">
          <TunersTab />
        </TabsContent>
        <TabsContent value="channels">
          <ChannelsTab />
        </TabsContent>
        <TabsContent value="guide">
          <GuideTab />
        </TabsContent>
        <TabsContent value="recordings">
          <RecordingsTab />
        </TabsContent>
        <TabsContent value="transcoding">
          <LiveTVTranscodingTab />
        </TabsContent>
      </Tabs>
    </div>
  );
}
