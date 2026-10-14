import Original from "../../../components/TasteSeedBanner";
import { usePreviewMode } from "../usePreviewMode";
export default function TasteSeedBanner() {
  return usePreviewMode().active ? null : <Original />;
}
