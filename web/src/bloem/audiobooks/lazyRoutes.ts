// Lazily loaded audiobook route elements used by ./routes.tsx.
import { lazy } from "react";

export const BloemAudiobookItemRoute = lazy(() => import("./BloemAudiobookItemRoute"));
export const BloemAudiobookLibraryRoute = lazy(() => import("./BloemAudiobookLibraryRoute"));
