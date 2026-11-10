import { useMemo } from "react";
import { useBloemT } from "@/bloem/i18n";
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

// Product names, shown as they are in every language.
const NVENC_LABEL = "NVIDIA NVENC";
const QSV_LABEL = "Intel Quick Sync (QSV)";
const VAAPI_LABEL = "VA-API";

/**
 * Live TV transcoding policy. Broadcast is MPEG-2 with AC-3 audio, which native
 * TV players decode directly but browsers cannot, so those sessions are
 * re-encoded — and an encode that falls below realtime starves the player.
 * These settings exist to get the encode onto the GPU and keep it there.
 */
export function LiveTVTranscodingTab() {
  const bt = useBloemT();
  const form = useSettingsForm({ keys: useMemo(() => KEYS, []) });
  const tuners = useLiveTVTuners();

  // Device-side transcoding shipped only on the discontinued HDHomeRun EXTEND.
  // Current tuners ignore ?transcode= silently, so the option is shown as
  // unavailable rather than offered and quietly dropped.
  const deviceTranscodeTuners = (tuners.data ?? []).filter(
    (tuner) => (tuner.transcode_codecs ?? []).length > 0,
  );

  if (form.isLoading)
    return (
      <p className="text-muted-foreground text-sm">{bt("bloem.admin.liveTv.conversion.loading")}</p>
    );

  return (
    <div className="flex h-full flex-col">
      <div className="mb-6 space-y-2">
        <h2 className="text-xl font-medium">{bt("bloem.admin.liveTv.conversion.heading")}</h2>
        <p className="text-muted-foreground max-w-2xl text-sm leading-relaxed">
          {bt("bloem.admin.liveTv.conversion.intro")}
        </p>
      </div>

      <div className="flex-1 space-y-6">
        <FieldGroup label={bt("bloem.admin.liveTv.conversion.hardware")}>
          <SettingField
            label={bt("bloem.admin.liveTv.conversion.hwAccel.label")}
            type="select"
            hint={bt("bloem.admin.liveTv.conversion.hwAccel.hint")}
            options={[
              { value: "auto", label: bt("bloem.admin.liveTv.conversion.hwAccel.auto") },
              { value: "nvenc", label: NVENC_LABEL },
              { value: "qsv", label: QSV_LABEL },
              { value: "vaapi", label: VAAPI_LABEL },
              { value: "none", label: bt("bloem.admin.liveTv.conversion.hwAccel.software") },
            ]}
            value={form.getValue("livetv.hw_accel")}
            onChange={(v) => form.setValue("livetv.hw_accel", v)}
          />
          <SettingField
            label={bt("bloem.admin.liveTv.conversion.hwDecode.label")}
            type="select"
            hint={bt("bloem.admin.liveTv.conversion.hwDecode.hint")}
            options={[
              { value: "auto", label: bt("bloem.admin.liveTv.conversion.hwDecode.auto") },
              { value: "on", label: bt("bloem.admin.liveTv.conversion.hwDecode.on") },
              { value: "off", label: bt("bloem.admin.liveTv.conversion.hwDecode.off") },
            ]}
            value={form.getValue("livetv.hw_decode")}
            onChange={(v) => form.setValue("livetv.hw_decode", v)}
          />
          <SettingField
            label={bt("bloem.admin.liveTv.conversion.preset.label")}
            type="select"
            hint={bt("bloem.admin.liveTv.conversion.preset.hint")}
            options={[
              {
                value: "low_latency",
                label: bt("bloem.admin.liveTv.conversion.preset.lowLatency"),
              },
              { value: "balanced", label: bt("bloem.admin.liveTv.conversion.preset.balanced") },
              { value: "quality", label: bt("bloem.admin.liveTv.conversion.preset.quality") },
            ]}
            value={form.getValue("livetv.encoder_preset")}
            onChange={(v) => form.setValue("livetv.encoder_preset", v)}
          />
          <SettingField
            label={bt("bloem.admin.liveTv.conversion.maxTranscodes.label")}
            type="number"
            hint={bt("bloem.admin.liveTv.conversion.maxTranscodes.hint")}
            value={form.getValue("livetv.max_transcodes")}
            onChange={(v) => form.setValue("livetv.max_transcodes", v)}
          />
        </FieldGroup>

        <FieldGroup label={bt("bloem.admin.liveTv.conversion.output")}>
          <SettingField
            label={bt("bloem.admin.liveTv.conversion.frameRate.label")}
            type="select"
            hint={bt("bloem.admin.liveTv.conversion.frameRate.hint")}
            options={[
              { value: "source", label: bt("bloem.admin.liveTv.conversion.frameRate.source") },
              {
                value: "60",
                label: bt("bloem.admin.liveTv.conversion.frameRate.fps", { rate: 60 }),
              },
              {
                value: "30",
                label: bt("bloem.admin.liveTv.conversion.frameRate.fps", { rate: 30 }),
              },
            ]}
            value={form.getValue("livetv.framerate_cap")}
            onChange={(v) => form.setValue("livetv.framerate_cap", v)}
          />
          <SettingField
            label={bt("bloem.admin.liveTv.conversion.maxResolution.label")}
            type="select"
            hint={bt("bloem.admin.liveTv.conversion.maxResolution.hint")}
            options={[
              { value: "source", label: bt("bloem.admin.liveTv.conversion.maxResolution.source") },
              { value: "1080p", label: "1080p" },
              { value: "720p", label: "720p" },
            ]}
            value={form.getValue("livetv.max_resolution")}
            onChange={(v) => form.setValue("livetv.max_resolution", v)}
          />
          <SettingField
            label={bt("bloem.admin.liveTv.conversion.playMethod.label")}
            type="select"
            hint={bt("bloem.admin.liveTv.conversion.playMethod.hint")}
            options={[
              { value: "auto", label: bt("bloem.admin.liveTv.conversion.playMethod.auto") },
              { value: "copy", label: bt("bloem.admin.liveTv.conversion.playMethod.copy") },
              {
                value: "transcode",
                label: bt("bloem.admin.liveTv.conversion.playMethod.transcode"),
              },
            ]}
            value={form.getValue("livetv.play_method")}
            onChange={(v) => form.setValue("livetv.play_method", v)}
          />
        </FieldGroup>

        <FieldGroup label={bt("bloem.admin.liveTv.conversion.tuner.heading")}>
          <div className="space-y-3 py-2">
            <p className="text-muted-foreground text-sm leading-relaxed">
              {bt("bloem.admin.liveTv.conversion.tuner.intro")}
            </p>
            {tuners.isLoading ? (
              <p className="text-muted-foreground text-sm">
                {bt("bloem.admin.liveTv.conversion.tuner.checking")}
              </p>
            ) : (tuners.data ?? []).length === 0 ? (
              <p className="text-muted-foreground text-sm">
                {bt("bloem.admin.liveTv.conversion.tuner.none")}
              </p>
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
                          {bt("bloem.admin.liveTv.conversion.tuner.notSupported")}
                        </Badge>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
            {deviceTranscodeTuners.length === 0 && (tuners.data ?? []).length > 0 && (
              <p className="text-muted-foreground text-xs">
                {bt("bloem.admin.liveTv.conversion.tuner.serverConverts")}
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
