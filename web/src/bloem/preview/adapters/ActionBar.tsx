/* eslint-disable react-refresh/only-export-components -- Preserve the original module exports at this adapter boundary. */
import type { ComponentProps, ReactNode } from "react";
import Original from "../../../pages/ItemDetail/components/ActionBar";
import { usePreviewMode } from "../usePreviewMode";
import { itemCtaLabel } from "../wording";
import type { FileVersion } from "@/api/types";
export * from "../../../pages/ItemDetail/components/ActionBar";
function MetadataPopover({ label, children }: { label: string; children: ReactNode }) {
  return (
    <details className="relative">
      <summary className="bg-muted cursor-pointer rounded-full px-4 py-2 text-sm">{label}</summary>
      <div className="bg-popover text-popover-foreground absolute z-30 mt-2 max-h-72 w-72 overflow-y-auto rounded-xl border p-4 shadow-lg">
        {children}
      </div>
    </details>
  );
}
function TechnicalMetadata({
  versions,
  selected,
}: {
  versions: FileVersion[];
  selected?: FileVersion | null;
}) {
  const version = selected ?? versions[0];
  return (
    <div className="flex flex-wrap gap-2">
      {versions.length > 0 && (
        <MetadataPopover label="Version">
          <ul className="space-y-2">
            {versions.map((value, index) => (
              <li key={value.file_id ?? index}>
                {[value.resolution, value.codec_video, value.hdr ? "HDR" : "", value.container]
                  .filter(Boolean)
                  .join(" · ") || `Version ${index + 1}`}
              </li>
            ))}
          </ul>
        </MetadataPopover>
      )}
      {Boolean(version?.audio_tracks?.length) && (
        <MetadataPopover label="Audio">
          <ul className="space-y-2">
            {version?.audio_tracks?.map((track, index) => (
              <li key={index}>
                {[track.language, track.codec, track.title].filter(Boolean).join(" · ") ||
                  `Audio ${index + 1}`}
              </li>
            ))}
          </ul>
        </MetadataPopover>
      )}
      {Boolean(version?.subtitle_tracks?.length) && (
        <MetadataPopover label="Subtitles">
          <ul className="space-y-2">
            {version?.subtitle_tracks?.map((track, index) => (
              <li key={index}>
                {[track.language, track.title].filter(Boolean).join(" · ") ||
                  `Subtitle ${index + 1}`}
              </li>
            ))}
          </ul>
        </MetadataPopover>
      )}
    </div>
  );
}
export default function ActionBar(props: ComponentProps<typeof Original>) {
  const mode = usePreviewMode();
  if (!mode.active) return <Original {...props} />;
  return (
    <div className="space-y-3">
      <Original
        contentId={props.contentId}
        itemTitle={props.itemTitle}
        compactMobile={props.compactMobile}
        canAddToCollection={false}
        links={props.links}
        primaryAction={{
          label: itemCtaLabel(mode.presentation, "watch"),
          onClick: () => mode.requestSubscription(props.contentId),
        }}
      />
      <TechnicalMetadata versions={props.versions ?? []} selected={props.selectedVersion} />
    </div>
  );
}
