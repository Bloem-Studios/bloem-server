import { usePreviewMode } from "../preview/usePreviewMode";
import { useEffect, useMemo, type ReactElement } from "react";
import { useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import type { ItemDetail } from "@/api/types";
import { isNotFoundProblem } from "@/api/v2/request";
import { parseOptionalLibraryId } from "@/components/sidebarItemNavigation";
import { useSidebarItemDetailsReady } from "@/components/sidebarItemNavigationContext";
import { useCatalogItemDetail } from "@/hooks/queries/catalogRead";
import { useDocumentTitle } from "@/hooks/useDocumentTitle";
import AudiobookContent from "@/pages/ItemDetail/AudiobookContent";
import ItemDetailSkeleton from "@/pages/ItemDetail/ItemDetailSkeleton";
import { useThemeMusic } from "@/pages/ItemDetail/useThemeMusic";
import PresentationPicker from "./PresentationPicker";
import {
  isApproximateResume,
  itemAudiobookPresentations,
  itemForPresentation,
  ownsAudiobookItem,
} from "./presentations";
import { useAudiobookPresentationChoice } from "./useAudiobookPresentationChoice";

/**
 * Element for /item/:id. An audiobook with more than one complete recording,
 * or any native one, shows a recording picker and Silo's AudiobookContent with
 * only the chosen recording's tracks. Every other item and every loading,
 * error and unavailable state is Silo's ItemDetail, passed in unchanged.
 *
 * It reads the item through the same query hook and key as ItemDetail, so the
 * route adds no request.
 */
export default function BloemAudiobookItemRoute({ itemDetail }: { itemDetail: ReactElement }) {
  const preview = usePreviewMode().active;
  const { id } = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();
  const libraryId = parseOptionalLibraryId(searchParams.get("libraryId"));
  const { data, isLoading, error } = useCatalogItemDetail(id, libraryId);
  const detailsReady = useSidebarItemDetailsReady();
  const item = isNotFoundProblem(error) ? undefined : data;
  const owned = useMemo(
    () => item?.type === "audiobook" && ownsAudiobookItem(itemAudiobookPresentations(item)),
    [item],
  );

  if (preview || isLoading || !detailsReady || !item || !owned) {
    return itemDetail;
  }
  return <PresentationItemDetail item={item} libraryId={libraryId} error={error} />;
}

function PresentationItemDetail({
  item,
  libraryId,
  error,
}: {
  item: ItemDetail;
  libraryId?: number;
  error: unknown;
}) {
  useDocumentTitle(item.title);
  useThemeMusic(item, false);
  useEffect(() => {
    // Matches ItemDetail: a failed refetch keeps the cached page and says so.
    if (error && !isNotFoundProblem(error)) {
      toast.error(error instanceof Error ? error.message : "Failed to load item");
    }
  }, [error]);

  const { presentations, chosen, choose, nativeAvailable, pending } =
    useAudiobookPresentationChoice(item);
  const view = useMemo(() => itemForPresentation(item, chosen), [chosen, item]);

  if (pending) {
    return <ItemDetailSkeleton />;
  }

  return (
    // The picker comes first for keyboard and screen-reader order, and is
    // drawn between the hero and the supporting content. AudiobookContent's
    // wrappers become `contents` so its hero and body are items of this
    // column; if Silo's markup changes, the picker only moves on screen.
    <div className="flex flex-col [&_.detail-supporting-content]:order-2">
      <PresentationPicker
        className="order-1"
        presentations={presentations}
        chosen={chosen}
        nativeAvailable={nativeAvailable}
        onChoose={choose}
        approximateResume={isApproximateResume(item, chosen)}
      />
      <div className="contents [&>div]:contents">
        <AudiobookContent item={view as ItemDetail & { type: "audiobook" }} libraryId={libraryId} />
      </div>
    </div>
  );
}
