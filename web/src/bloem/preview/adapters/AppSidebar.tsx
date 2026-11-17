import type { ComponentProps } from "react";

// Layout.tsx imports its sidebar through this adapter, which carries Bloem's
// phone header rule into Layout as well.
import "@/bloem/mobile-header.css";

import Original from "@/components/AppSidebar";
import PreviewSidebar from "../PreviewSidebarBoundary";
import { usePreviewMode } from "../usePreviewMode";

/** Layout's sidebar: the preview's own while a guest previews, Silo's otherwise. */
export default function AppSidebar(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <PreviewSidebar {...props} /> : <Original {...props} />;
}
