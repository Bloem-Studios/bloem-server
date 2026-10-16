import type { ComponentProps } from "react";
import Original from "../../audiobooks/BloemLibraryRecommended";
import { usePreviewMode } from "../usePreviewMode";
import { RecommendationsUnavailable } from "./LibraryRecommended";
// The audiobook library's opening deck is built from the same personal feed.
export default function BloemLibraryRecommended(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <RecommendationsUnavailable /> : <Original {...props} />;
}
