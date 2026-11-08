import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { Plus, Radar, RefreshCw, Trash2, X } from "lucide-react";
import type { LiveTVDiscoveredTuner } from "@/api/bloemTypes";
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
import { LiveTVTranscodingTab } from "@/pages/livetv/LiveTVTranscodingTab";
import { XtreamProviderForm } from "@/components/livetv/XtreamProviderForm";
import { XtreamGuideSourceForm } from "@/components/livetv/XtreamGuideSourceForm";
import { XtreamRemoveButton } from "@/components/livetv/XtreamRemoveButton";

const LIVETV_TABS = ["tuners", "channels", "guide", "recordings", "transcoding"] as const;
type LiveTVTab = (typeof LIVETV_TABS)[number];

function normalizeTab(value: string | null): LiveTVTab {
  return LIVETV_TABS.includes(value as LiveTVTab) ? (value as LiveTVTab) : "tuners";
}

function readable(value: string): string {
  const text = value.replace(/_/g, " ").trim();
  return text ? text[0]!.toUpperCase() + text.slice(1) : value;
}

function count(value: number, noun: string): string {
  return `${value.toLocaleString()} ${value === 1 ? noun : `${noun}s`}`;
}

function kindLabel(kind: string): string {
  if (kind === "dispatcharr") return "Dispatcharr";
  if (kind === "hdhomerun") return "HDHomeRun";
  return kind || "Tuner";
}

function TunersTab() {
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
          Your tuners
        </h2>
        {tuners.isLoading ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : (tuners.data?.length ?? 0) === 0 ? (
          <p className="text-muted-foreground text-sm">
            None yet. Add an antenna tuner or an Xtream provider below.
          </p>
        ) : (
          <ul className="divide-border divide-y border-y">
            {tuners.data?.map((tuner) => (
              <li key={tuner.id} className="flex flex-wrap items-center justify-between gap-3 py-4">
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{tuner.model || tuner.device_id}</span>
                    <Badge variant="secondary">{readable(tuner.status)}</Badge>
                    <span className="text-muted-foreground text-sm tabular-nums">
                      {tuner.channel_count} {tuner.channel_count === 1 ? "channel" : "channels"}
                    </span>
                  </div>
                  <p className="text-muted-foreground truncate font-mono text-xs">
                    {tuner.base_url || tuner.device_id}
                  </p>
                  {tuner.last_error ? (
                    <p className="text-destructive text-xs">Last problem: {tuner.last_error}</p>
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
                    Find channels again
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
                      Remove
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
          title="Remove this tuner?"
          description="Its channels disappear from Live TV and the guide. You can add the tuner again later."
          confirmLabel="Remove tuner"
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
            Add an antenna tuner
          </h2>
          <p className="text-muted-foreground mt-1 text-sm">
            HDHomeRun tuners, and Dispatcharr through its HDHomeRun mode. Bloem finds the channels
            once the tuner is added.
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="tuner-url">Tuner address</Label>
          <Input
            id="tuner-url"
            placeholder="http://192.168.1.50"
            value={tunerURL}
            onChange={(e) => setTunerURL(e.target.value)}
          />
          <p className="text-muted-foreground text-xs">
            Optional for a search. For Dispatcharr, use its address, like
            http://dispatcharr.local:9191.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button onClick={submit} disabled={addTuner.isPending || !tunerURL.trim()}>
            <Plus />
            {addTuner.isPending ? "Adding…" : "Add tuner"}
          </Button>
          <Button variant="outline" onClick={runDiscovery} disabled={discoverTuners.isPending}>
            <Radar />
            {discoverTuners.isPending ? "Searching…" : "Search my network"}
          </Button>
        </div>
        {searched && candidates.length === 0 ? (
          <p className="text-muted-foreground text-sm">No tuners found.</p>
        ) : null}
        {candidates.length > 0 ? (
          <ul className="divide-border divide-y border-y" aria-label="Tuners found">
            {candidates.map((c) => (
              <li
                key={`${c.device_id}-${c.base_url || c.discover_url}`}
                className="flex flex-wrap items-center justify-between gap-3 py-3"
              >
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">
                      {c.friendly_name || c.model || c.device_id || "Tuner"}
                    </span>
                    <span className="text-muted-foreground text-sm">
                      {kindLabel(c.kind)}
                      {c.tuner_count > 0
                        ? ` · ${c.tuner_count} ${c.tuner_count === 1 ? "tuner" : "tuners"}`
                        : ""}
                    </span>
                  </div>
                  <p className="text-muted-foreground truncate font-mono text-xs">
                    {c.base_url || c.discover_url}
                  </p>
                </div>
                {c.already_added ? (
                  <span className="text-muted-foreground text-sm">Added</span>
                ) : (
                  <Button
                    size="sm"
                    disabled={addTuner.isPending || !(c.base_url || c.discover_url)}
                    onClick={() => addCandidate(c)}
                  >
                    <Plus />
                    Add
                  </Button>
                )}
              </li>
            ))}
          </ul>
        ) : null}
        {discoverNotes.length > 0 ? (
          <ul className="text-muted-foreground list-disc space-y-1 pl-5 text-xs">
            {discoverNotes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        ) : null}
        <details className="text-sm">
          <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
            Search doesn’t find your tuner?
          </summary>
          <p className="text-muted-foreground mt-2">
            The network search listens for tuners announcing themselves, which Docker’s default
            network setup blocks. Type the tuner’s address and choose Add tuner instead, or run
            Bloem with host networking.
          </p>
        </details>
      </section>

      <section className="max-w-2xl" aria-label="Add an Xtream provider">
        <XtreamProviderForm />
      </section>
    </div>
  );
}

function ChannelsTab() {
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
      <p className="text-muted-foreground text-sm">
        Turn on the channels people can watch. Once a guide source has synced, Bloem matches each
        channel to its listings by number and call sign. Fix a match only if it’s wrong.
      </p>
      {sorted.length > 0 ? (
        <p className="text-muted-foreground text-xs">
          {mappedCount} of {sorted.length} channels have listings
          {mappedCount < sorted.length ? ". Sync a guide source to match the rest." : "."}
        </p>
      ) : null}
      {channels.isLoading ? (
        <p className="text-muted-foreground text-sm">Loading…</p>
      ) : sorted.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No channels yet. Add a tuner on the Tuners tab first.
        </p>
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
                      {channel.number_override || channel.number} ·{" "}
                      {channel.callsign || channel.name}
                    </span>
                    {channel.hd ? <Badge variant="secondary">HD</Badge> : null}
                    {mapped ? (
                      <Badge variant="outline" title={`Station ${channel.guide_station_id}`}>
                        Listings matched
                      </Badge>
                    ) : (
                      <Badge variant="secondary">No listings</Badge>
                    )}
                  </div>
                  {editing ? (
                    <div className="flex flex-wrap items-end gap-2">
                      <div className="space-y-1">
                        <Label htmlFor={`station-${channel.id}`} className="text-xs">
                          Station ID from your guide source
                        </Label>
                        <Input
                          id={`station-${channel.id}`}
                          className="w-48"
                          placeholder="12345"
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
                        Done
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
                      {mapped ? "Fix listings match" : "Match listings by hand"}
                    </Button>
                  )}
                </div>
                <div className="flex items-center gap-2 self-start sm:self-center">
                  <Label
                    htmlFor={`enabled-${channel.id}`}
                    className="text-muted-foreground text-sm"
                  >
                    Show in Live TV
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
  const sources = useLiveTVGuideSources();
  const createSource = useCreateLiveTVGuideSource();
  const updateSource = useUpdateLiveTVGuideSource();
  const deleteSource = useDeleteLiveTVGuideSource();
  const syncSource = useSyncLiveTVGuideSource();
  const lookupSDLineups = useLookupSchedulesDirectLineups();
  const lookupXMLLineups = useLookupXMLSyncLineups();

  const [sdDisplayName, setSDDisplayName] = useState("Schedules Direct");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [sdCountry, setSDCountry] = useState("USA");
  const [sdPostalCode, setSDPostalCode] = useState("");
  const [sdLineup, setSDLineup] = useState("");
  const [sdLineups, setSDLineups] = useState<SchedulesDirectLineupOption[]>([]);

  const [xmlDisplayName, setXMLDisplayName] = useState("Free listings");
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
        display_name: sdDisplayName.trim() || "Schedules Direct",
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
        display_name: xmlDisplayName.trim() || "Free listings",
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
            Guide sources
          </h2>
          <p className="text-muted-foreground mt-1 max-w-2xl text-sm">
            Where the TV guide comes from. Up to three can be on at once.
          </p>
        </div>
        {sources.isLoading ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : (sources.data?.length ?? 0) === 0 ? (
          <p className="text-muted-foreground text-sm">None yet. Add one below.</p>
        ) : (
          <ul className="divide-border divide-y border-y">
            {sources.data?.map((source) => (
              <li
                key={source.id}
                className="flex flex-wrap items-center justify-between gap-3 py-4"
              >
                <div className="min-w-0 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">
                      {source.display_name || sourceTypeLabel(source.type)}
                    </span>
                    <span className="text-muted-foreground text-sm">
                      {sourceTypeLabel(source.type)}
                    </span>
                    <Badge variant="outline">{readable(source.status)}</Badge>
                  </div>
                  <p className="text-muted-foreground truncate text-xs">
                    {source.config?.postalcode ? `${source.config.postalcode} · ` : ""}
                    {source.last_sync_at
                      ? `Last synced ${new Date(source.last_sync_at).toLocaleString()}`
                      : "Not synced yet"}
                  </p>
                  {source.last_error ? (
                    <p className="text-destructive text-xs">Last problem: {source.last_error}</p>
                  ) : null}
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <div className="flex items-center gap-2">
                    <Label
                      htmlFor={`source-enabled-${source.id}`}
                      className="text-muted-foreground text-sm"
                    >
                      On
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
                    Sync now
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={deleteSource.isPending}
                    onClick={() => setRemoving(source.id)}
                  >
                    <Trash2 />
                    Remove
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
        <ConfirmDialog
          open={removing !== null}
          onOpenChange={(open) => !open && setRemoving(null)}
          title="Remove this guide source?"
          description="Channels that only it covered lose their listings until another source covers them."
          confirmLabel="Remove source"
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
          Add a guide source
        </h2>
        {atLimit ? (
          <p className="border-border bg-muted/40 rounded-lg border p-3 text-sm">
            Three guide sources are on. Turn one off to add another.
          </p>
        ) : null}
        <fieldset className="grid gap-2" role="radiogroup" aria-label="Kind of guide source">
          {(
            [
              ["xml", "Free listings", "By postal code. No account needed."],
              ["sd", "Schedules Direct", "A paid listings account."],
              ["xtream", "Xtream provider", "The guide that comes with your Xtream provider."],
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
              Enter your postal code, find your lineup, then add it.
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="xml-country">Country</Label>
                <Input
                  id="xml-country"
                  value={xmlCountry}
                  onChange={(e) => setXMLCountry(e.target.value)}
                  placeholder="USA"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="xml-postal">Postal or ZIP code</Label>
                <Input
                  id="xml-postal"
                  value={xmlPostalCode}
                  onChange={(e) => setXMLPostalCode(e.target.value)}
                  placeholder="12345"
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
              {lookupXMLLineups.isPending ? "Looking up…" : "Find lineups"}
            </Button>
            {xmlLineups.length > 0 ? (
              <div className="space-y-1.5">
                <Label htmlFor="xml-lineup">Lineup</Label>
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
                      {item.name}
                      {item.location ? ` (${item.location})` : ""} · {item.transport}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <details className="text-sm">
                <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
                  Enter a lineup ID instead
                </summary>
                <div className="mt-3 grid gap-4 sm:grid-cols-2">
                  <div className="space-y-1.5">
                    <Label htmlFor="xml-lineup-manual">Lineup ID</Label>
                    <Input
                      id="xml-lineup-manual"
                      value={xmlLineup}
                      onChange={(e) => setXMLLineup(e.target.value)}
                      placeholder="USA-lineupId-DEFAULT"
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="xml-headend-manual">Headend ID</Label>
                    <Input
                      id="xml-headend-manual"
                      value={xmlHeadend}
                      onChange={(e) => setXMLHeadend(e.target.value)}
                      placeholder="lineupId"
                    />
                  </div>
                </div>
              </details>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="xml-guide-name">Name</Label>
              <Input
                id="xml-guide-name"
                value={xmlDisplayName}
                onChange={(e) => setXMLDisplayName(e.target.value)}
              />
            </div>
            <Button onClick={addXMLSync} disabled={createSource.isPending || !canAddXML}>
              <Plus />
              {createSource.isPending ? "Adding…" : "Add free listings"}
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
              Sign in with your{" "}
              <a
                href="https://www.schedulesdirect.org/"
                target="_blank"
                rel="noreferrer"
                className="underline underline-offset-2"
              >
                Schedules Direct
              </a>{" "}
              account, find your lineup, then add it.
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="sd-username">Username</Label>
                <Input
                  id="sd-username"
                  autoComplete="username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sd-password">Password</Label>
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
                <Label htmlFor="sd-country">Country</Label>
                <Input
                  id="sd-country"
                  value={sdCountry}
                  onChange={(e) => setSDCountry(e.target.value)}
                  placeholder="USA"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="sd-postal">Postal or ZIP code</Label>
                <Input
                  id="sd-postal"
                  value={sdPostalCode}
                  onChange={(e) => setSDPostalCode(e.target.value)}
                  placeholder="12345"
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
              {lookupSDLineups.isPending ? "Looking up…" : "Find lineups"}
            </Button>
            {sdLineups.length > 0 ? (
              <div className="space-y-1.5">
                <Label htmlFor="sd-lineup">Lineup</Label>
                <select
                  id="sd-lineup"
                  className="border-input bg-background h-9 w-full rounded-md border px-3 text-sm"
                  value={sdLineup}
                  onChange={(e) => setSDLineup(e.target.value)}
                >
                  {sdLineups.map((item) => (
                    <option key={item.lineup} value={item.lineup}>
                      {item.name} · {item.transport}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <details className="text-sm">
                <summary className="text-muted-foreground hover:text-foreground cursor-pointer transition-colors">
                  Enter a lineup ID instead
                </summary>
                <div className="mt-3 space-y-1.5">
                  <Label htmlFor="sd-lineup-manual">Lineup ID</Label>
                  <Input
                    id="sd-lineup-manual"
                    value={sdLineup}
                    onChange={(e) => setSDLineup(e.target.value)}
                    placeholder="USA-OTA-12345"
                  />
                </div>
              </details>
            )}
            <div className="space-y-1.5">
              <Label htmlFor="guide-name">Name</Label>
              <Input
                id="guide-name"
                value={sdDisplayName}
                onChange={(e) => setSDDisplayName(e.target.value)}
              />
            </div>
            <Button type="submit" disabled={createSource.isPending || !canAddSD}>
              <Plus />
              {createSource.isPending ? "Adding…" : "Add Schedules Direct"}
            </Button>
          </form>
        ) : null}
      </section>
    </div>
  );
}

function sourceTypeLabel(type: string): string {
  if (type === "xml_sync") return "Free listings";
  if (type === "schedules_direct") return "Schedules Direct";
  if (type === "xtream") return "Xtream provider";
  return readable(type);
}

function RecordingsTab() {
  const recordings = useLiveTVRecordings();
  const cancelRecording = useCancelLiveTVRecording();

  if (recordings.isLoading) {
    return <p className="text-muted-foreground text-sm">Loading…</p>;
  }
  if ((recordings.data?.length ?? 0) === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        No recordings yet. People schedule them from the guide.
      </p>
    );
  }

  return (
    <ul className="divide-border divide-y border-y">
      {recordings.data?.map((rec) => (
        <li key={rec.id} className="flex flex-wrap items-center justify-between gap-3 py-4">
          <div className="min-w-0 space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{rec.title || "Untitled"}</span>
              <Badge variant="secondary">{readable(rec.status)}</Badge>
            </div>
            <p className="text-muted-foreground text-xs">
              {new Date(rec.start).toLocaleString()} – {new Date(rec.stop).toLocaleString()}
              {rec.last_error ? ` · Problem: ${rec.last_error}` : ""}
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
              Cancel recording
            </Button>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

export default function AdminLiveTV() {
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
            <h1 className="page-title text-[clamp(2rem,4vw,3rem)]">Live TV</h1>
          </div>
          <p className="text-muted-foreground max-w-2xl text-sm leading-6">
            Watch and record TV: add an antenna tuner or Xtream provider, choose the channels, and
            add a guide source for the listings.
          </p>
          <p className="text-muted-foreground text-sm tabular-nums">
            {count(tuners.data?.length ?? 0, "tuner")} ·{" "}
            {count(channels.data?.length ?? 0, "channel")} ·{" "}
            {sources.data?.filter((s) => s.enabled).length ?? 0} of 3 guide sources on
          </p>
        </div>
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab} className="gap-5">
        <TabsList variant="line" className="border-border w-full justify-start border-b">
          <TabsTrigger value="tuners">Tuners</TabsTrigger>
          <TabsTrigger value="channels">Channels</TabsTrigger>
          <TabsTrigger value="guide">Guide</TabsTrigger>
          <TabsTrigger value="recordings">Recordings</TabsTrigger>
          <TabsTrigger value="transcoding">Conversion</TabsTrigger>
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
