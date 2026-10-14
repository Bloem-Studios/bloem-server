/* eslint-disable react-refresh/only-export-components -- Preserve the original module exports at this adapter boundary. */
import type { ComponentProps } from "react";
import { MoreHorizontal } from "lucide-react";
import { Link } from "react-router";
import Original, {
  PosterCardFavoriteButton as OriginalFavorite,
} from "../../../components/MediaItemMenu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { usePreviewMode } from "../usePreviewMode";
export * from "../../../components/MediaItemMenu";
export function PosterCardFavoriteButton(props: ComponentProps<typeof OriginalFavorite>) {
  return usePreviewMode().active ? null : <OriginalFavorite {...props} />;
}
export default function MediaItemMenu(props: ComponentProps<typeof Original>) {
  const mode = usePreviewMode();
  if (!mode.active) return <Original {...props} />;
  const query = props.libraryId ? `?libraryId=${props.libraryId}` : "";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label="More actions"
          className="absolute top-2 right-2 rounded-full bg-black/50 p-2 text-white"
          onClick={(event) => event.stopPropagation()}
        >
          <MoreHorizontal className="size-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuItem asChild>
          <Link to={`/item/${encodeURIComponent(props.contentId)}${query}`}>Details</Link>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
