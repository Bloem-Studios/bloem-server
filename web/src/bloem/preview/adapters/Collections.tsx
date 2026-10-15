import Collections from "@/pages/Collections";
import PreviewCollections from "../PreviewCollections";
import { usePreviewMode } from "../usePreviewMode";

export default function CollectionsWithPreview() {
  return usePreviewMode().active ? <PreviewCollections /> : <Collections />;
}
