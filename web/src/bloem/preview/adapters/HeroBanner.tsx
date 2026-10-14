import { lazy, Suspense, type ComponentProps } from "react";
import Original from "../../../components/HeroBanner";
import { usePreviewMode } from "../usePreviewMode";
const PreviewHero = lazy(() => import("../PreviewHero"));
export default function HeroBanner(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? (
    <Suspense fallback={null}>
      <PreviewHero {...props} />
    </Suspense>
  ) : (
    <Original {...props} />
  );
}
