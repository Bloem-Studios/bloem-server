import type { ComponentProps } from "react";
import Original from "../../../components/LibraryHeader";
import { usePreviewMode } from "../usePreviewMode";
type LibraryTabs = NonNullable<ComponentProps<typeof Original>["availableTabs"]>;
const ALL_TABS: LibraryTabs = ["recommended", "library", "collections"];
// Recommendations (the audiobook "Home" deck too) are personal and refused to a
// preview visitor, so a preview library has only its library and collections.
export default function LibraryHeader(props: ComponentProps<typeof Original>) {
  const preview = usePreviewMode().active;
  return (
    <Original
      {...props}
      availableTabs={
        preview
          ? (props.availableTabs ?? ALL_TABS).filter((tab) => tab !== "recommended")
          : props.availableTabs
      }
      onShuffle={preview ? undefined : props.onShuffle}
    />
  );
}
