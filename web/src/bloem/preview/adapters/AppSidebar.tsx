import type { ComponentProps } from "react";

import Original from "@/components/AppSidebar";
import PreviewSidebar from "../PreviewSidebarBoundary";
import { usePreviewMode } from "../usePreviewMode";

/** Layout's sidebar: the preview's own while a guest previews, Silo's otherwise. */
export default function AppSidebar(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <PreviewSidebar {...props} /> : <Original {...props} />;
}
