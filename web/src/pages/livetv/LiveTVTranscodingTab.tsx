import { useMemo } from "react";
import { Badge } from "@/components/ui/badge";
import { useLiveTVTuners } from "@/hooks/queries/useLiveTV";
import { useSettingsForm } from "@/hooks/useSettingsForm";
import { FieldGroup } from "@/pages/admin-settings/FieldGroup";
import { SaveBar } from "@/components/SaveBar";
import { SettingField } from "@/pages/admin-settings/SettingField";

const KEYS = [
  "livetv.hw_accel",
  "livetv.hw_decode",
  "livetv.encoder_preset",
  "livetv.framerate_cap",
  "livetv.max_resolution",
  "livetv.play_method",
  "livetv.max_transcodes",
];

/**
 * Live TV transcoding policy. Broadcast is MPEG-2 with AC-3 audio, which native
 * TV players decode directly but browsers cannot, so those sessions are
 * re-encoded — and an encode that falls below realtime starves the player.
 * These settings exist to get the encode onto the GPU and keep it there.
 */
export function LiveTVTranscodingTab() {
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const tuners = useLiveTVTuners();

  // Device-side transcoding shipped only on the discontinued HDHomeRun EXTEND.
  // Current tuners ignore ?transcode= silently, so the option is shown as
  // unavailable rather than offered and quietly dropped.
  const deviceTranscodeTuners = (tuners.data ?? []).filter(
    (tuner) => (tuner.transcode_codecs ?? []).length > 0,
  );

  if (form.isLoading) return <p className="text-muted-foreground text-sm">Loading settings…</p>;

  return (
    <div className="flex h-full flex-col">
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-medium">Conversion</h2>
        <p className="text-muted-foreground max-w-2xl text-sm leading-relaxed">
          Devices that can play a broadcast get it as it is. For the rest, Bloem converts it as it
          plays, and the conversion has to keep up with the live feed.
        </p>
      </div>

      <div className="flex-1 space-y-6">
        <FieldGroup label="Hardware">
          <SettingField
            label="Hardware acceleration"
            type="select"
            hint="What converts live video. Auto picks what this server has."
            options={[
              { value: "auto", label: "Auto" },
              { value: "nvenc", label: "NVIDIA NVENC" },
              { value: "qsv", label: "Intel Quick Sync (QSV)" },
              { value: "vaapi", label: "VA-API" },
              { value: "none", label: "Software" },
            ]}
            value={form.getValue("livetv.hw_accel")}
            onChange={(v) => form.setValue("livetv.hw_accel", v)}
          />
          <SettingField
            label="Hardware decoding"
            type="select"
            hint="Read the broadcast on the graphics card. Reading it without one is the usual reason a conversion falls behind. Auto uses it when available."
            options={[
              { value: "auto", label: "Auto" },
              { value: "on", label: "Always on" },
              { value: "off", label: "Off (software decode)" },
            ]}
            value={form.getValue("livetv.hw_decode")}
            onChange={(v) => form.setValue("livetv.hw_decode", v)}
          />
          <SettingField
            label="Encoder preset"
            type="select"
            hint="Low latency keeps up with the feed. Quality looks better but may fall behind."
            options={[
              { value: "low_latency", label: "Low latency (recommended)" },
              { value: "balanced", label: "Balanced" },
              { value: "quality", label: "Quality" },
            ]}
            value={form.getValue("livetv.encoder_preset")}
            onChange={(v) => form.setValue("livetv.encoder_preset", v)}
          />
          <SettingField
            label="Conversions at once"
            type="number"
            hint="Channels converted at the same time. Channels sent as they are don’t count. 0 means the default of 3; -1 means no limit."
            value={form.getValue("livetv.max_transcodes")}
            onChange={(v) => form.setValue("livetv.max_transcodes", v)}
          />
        </FieldGroup>

        <FieldGroup label="Output">
          <SettingField
            label="Frame rate cap"
            type="select"
            hint="Only for hardware that can’t keep up. It halves smoothness, so leave it on Source unless conversions fall behind."
            options={[
              { value: "source", label: "Source (recommended)" },
              { value: "60", label: "60fps" },
              { value: "30", label: "30fps" },
            ]}
            value={form.getValue("livetv.framerate_cap")}
            onChange={(v) => form.setValue("livetv.framerate_cap", v)}
          />
          <SettingField
            label="Maximum resolution"
            type="select"
            hint="The largest size for converted video. Never makes video bigger than the broadcast."
            options={[
              { value: "source", label: "Source" },
              { value: "1080p", label: "1080p" },
              { value: "720p", label: "720p" },
            ]}
            value={form.getValue("livetv.max_resolution")}
            onChange={(v) => form.setValue("livetv.max_resolution", v)}
          />
          <SettingField
            label="Play method"
            type="select"
            hint="Auto checks what each device can play. Always send as is skips conversion, which web browsers can’t play. Always convert converts for everyone."
            options={[
              { value: "auto", label: "Auto (recommended)" },
              { value: "copy", label: "Always send as is" },
              { value: "transcode", label: "Always convert" },
            ]}
            value={form.getValue("livetv.play_method")}
            onChange={(v) => form.setValue("livetv.play_method", v)}
          />
        </FieldGroup>

        <FieldGroup label="Conversion on the tuner">
          <div className="space-y-3 py-2">
            <p className="text-muted-foreground text-sm leading-relaxed">
              Some older HDHomeRun models (EXTEND) can convert video themselves. Bloem uses this
              only when a tuner says it can.
            </p>
            {tuners.isLoading ? (
              <p className="text-muted-foreground text-sm">Checking tuners…</p>
            ) : (tuners.data ?? []).length === 0 ? (
              <p className="text-muted-foreground text-sm">No tuners yet.</p>
            ) : (
              <ul className="divide-border divide-y border-y">
                {(tuners.data ?? []).map((tuner) => {
                  const profiles = tuner.transcode_codecs ?? [];
                  return (
                    <li
                      key={tuner.id}
                      className="flex flex-wrap items-center justify-between gap-2 py-2"
                    >
                      <span className="text-sm font-medium">
                        {tuner.model || tuner.device_id || tuner.id}
                      </span>
                      {profiles.length > 0 ? (
                        <span className="flex flex-wrap items-center gap-1.5">
                          {profiles.map((profile) => (
                            <Badge key={profile} variant="secondary">
                              {profile}
                            </Badge>
                          ))}
                        </span>
                      ) : (
                        <Badge variant="outline" className="text-muted-foreground">
                          Not supported by this tuner
                        </Badge>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
            {deviceTranscodeTuners.length === 0 && (tuners.data ?? []).length > 0 && (
              <p className="text-muted-foreground text-xs">
                No tuner can convert video itself, so Bloem converts on the server.
              </p>
            )}
          </div>
        </FieldGroup>
      </div>

      <SaveBar
        dirtyCount={form.dirtyCount}
        onSave={form.save}
        onDiscard={form.discard}
        isSaving={form.isSaving}
      />
    </div>
  );
}
