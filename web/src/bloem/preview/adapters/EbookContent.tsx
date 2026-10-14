import type { ComponentProps } from "react";
import Original from "../../../pages/ItemDetail/EbookContent";
import { usePreviewMode } from "../usePreviewMode";
import PreviewBookDetail from "../PreviewBookDetail";
export default function EbookContent(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <PreviewBookDetail {...props} /> : <Original {...props} />;
}
