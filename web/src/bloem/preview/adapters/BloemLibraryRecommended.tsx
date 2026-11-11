import type { ComponentProps } from "react";
import Original from "../../audiobooks/BloemLibraryRecommended";
import LibraryRecommended from "@/pages/LibraryRecommended";
import { usePreviewMode } from "../usePreviewMode";
// An audiobook library's opening page is Silo's library rows with a Bloem
// continue-listening hero, which is personal. A preview gets Silo's page: the
// same rows, which the server serves a preview without the personal ones, and
// no continue-listening hero.
export default function BloemLibraryRecommended(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <LibraryRecommended {...props} /> : <Original {...props} />;
}
