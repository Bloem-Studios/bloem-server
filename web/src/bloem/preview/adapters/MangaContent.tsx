import type { ComponentProps } from "react";
import Original from "../../../pages/ItemDetail/MangaContent";
import { usePreviewMode } from "../usePreviewMode";
import PreviewBookDetail from "../PreviewBookDetail";
export default function MangaContent(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <PreviewBookDetail {...props} /> : <Original {...props} />;
}
