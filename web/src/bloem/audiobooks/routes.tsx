// Route elements for audiobook presentations, spliced into the Silo-owned
// App.tsx route table. Each wraps the Silo page element App already builds
// (its own lazy chunk, warmed by App's hot-route prefetch) and stays lazy
// itself, so the eager bundle does not grow.
import type { ReactElement } from "react";
import { BloemAudiobookItemRoute, BloemAudiobookLibraryRoute } from "./lazyRoutes";

/** /item/:id: Silo's ItemDetail, except audiobooks with several or native recordings. */
export function audiobookItemRoute(itemDetail: ReactElement) {
  return <BloemAudiobookItemRoute itemDetail={itemDetail} />;
}

/** /library/:libraryId: Silo's LibraryPage, except an audiobook library's home tab. */
export function audiobookLibraryRoute(libraryPage: ReactElement) {
  return <BloemAudiobookLibraryRoute libraryPage={libraryPage} />;
}
