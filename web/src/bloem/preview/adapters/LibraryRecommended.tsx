import type { ComponentProps } from "react";
import Original from "../../../pages/LibraryRecommended";
import PageUnavailable from "../../../components/PageUnavailable";
import { usePreviewMode } from "../usePreviewMode";
// A library's recommendations are personal, so the server refuses them to a
// preview visitor. The tab says so plainly rather than reporting the refusal as
// a failure with a Try again that can never succeed.
export function RecommendationsUnavailable() {
  return (
    <PageUnavailable
      title="Recommendations aren't available to preview members"
      description="You can still browse everything in this library."
    />
  );
}
export default function LibraryRecommended(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <RecommendationsUnavailable /> : <Original {...props} />;
}
