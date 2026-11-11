import type { ComponentProps } from "react";
import { Navigate, useSearchParams } from "react-router";
import Original from "../../audiobooks/BloemLibraryRecommended";
import { usePreviewMode } from "../usePreviewMode";
// The audiobook library's opening deck is built from the personal feed, so its
// header leaves the Recommended tab out for a preview (LibraryHeader.tsx). A
// visit that still lands on it (the default tab, an old link) goes to the
// browsable library tab, keeping the rest of the address. Other libraries show
// a preview their Recommended rows: the server leaves the personal ones out.
export function OpenLibraryTab() {
  const [searchParams] = useSearchParams();
  const next = new URLSearchParams(searchParams);
  next.set("tab", "library");
  return <Navigate to={{ search: `?${next}` }} replace />;
}
export default function BloemLibraryRecommended(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <OpenLibraryTab /> : <Original {...props} />;
}
