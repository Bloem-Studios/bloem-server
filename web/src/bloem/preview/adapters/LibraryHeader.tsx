import type { ComponentProps } from "react";
import Original from "../../../components/LibraryHeader";
import { usePreviewMode } from "../usePreviewMode";
// A library's Recommended tab (an audiobook library's Home) is its row layout,
// which the server serves a preview without the personal rows (Continue
// Watching, For You and the like), so a preview keeps every tab. Shuffle
// starts playback, which a preview cannot.
//
// At phone width, with large text, three tabs can outgrow the row: they
// tighten there, and the bar scrolls rather than running off the screen's edge.
const PHONE_TABS =
  "contents [&_.marquee-tab-bar]:max-w-full [&_.marquee-tab-bar]:min-w-0 [&_.marquee-tab-bar]:overflow-x-auto [&_.marquee-tab-bar]:[scrollbar-width:none] [&_.marquee-tab-trigger]:shrink-0 [&_.marquee-tab-trigger]:whitespace-nowrap max-sm:[&_.marquee-tab-trigger]:px-3";

export default function LibraryHeader(props: ComponentProps<typeof Original>) {
  const preview = usePreviewMode().active;
  return (
    <div className={PHONE_TABS}>
      <Original {...props} onShuffle={preview ? undefined : props.onShuffle} />
    </div>
  );
}
