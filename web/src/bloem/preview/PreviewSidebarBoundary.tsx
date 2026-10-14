import { lazy, Suspense } from "react";
import type { ComponentProps } from "react";
import type Original from "./PreviewSidebar";
const PreviewSidebar = lazy(() => import("./PreviewSidebar"));
export default function PreviewSidebarBoundary(props: ComponentProps<typeof Original>) {
  return (
    <Suspense fallback={null}>
      <PreviewSidebar {...props} />
    </Suspense>
  );
}
