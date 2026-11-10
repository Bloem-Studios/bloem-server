import type { ComponentProps } from "react";
import Original from "../../../components/LibraryHeader";
import { usePreviewMode } from "../usePreviewMode";
type LibraryTabs = NonNullable<ComponentProps<typeof Original>["availableTabs"]>;
const ALL_TABS: LibraryTabs = ["recommended", "library", "collections"];
// Recommendations (the audiobook "Home" deck too) are personal and refused to a
// preview visitor, so a preview library has only its library and collections.
//
// At phone width, with large text, three tabs can outgrow the row: they
// tighten there, and the bar scrolls rather than running off the screen's edge.
const PHONE_TABS =
  "contents [&_.marquee-tab-bar]:max-w-full [&_.marquee-tab-bar]:min-w-0 [&_.marquee-tab-bar]:overflow-x-auto [&_.marquee-tab-bar]:[scrollbar-width:none] [&_.marquee-tab-trigger]:shrink-0 [&_.marquee-tab-trigger]:whitespace-nowrap max-sm:[&_.marquee-tab-trigger]:px-3";

export default function LibraryHeader(props: ComponentProps<typeof Original>) {
  const preview = usePreviewMode().active;
  return (
    <div className={PHONE_TABS}>
      <Original
        {...props}
        availableTabs={
          preview
            ? (props.availableTabs ?? ALL_TABS).filter((tab) => tab !== "recommended")
            : props.availableTabs
        }
        onShuffle={preview ? undefined : props.onShuffle}
      />
    </div>
  );
}
