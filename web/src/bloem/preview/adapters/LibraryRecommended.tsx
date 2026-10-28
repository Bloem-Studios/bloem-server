import type { ComponentProps } from "react";
import { Navigate, useSearchParams } from "react-router";
import Original from "../../../pages/LibraryRecommended";
import { usePreviewMode } from "../usePreviewMode";
// A library's recommendations are personal, so the server refuses them to a
// preview visitor, and the header leaves the tab out. A visit that still lands
// on it (the library's default tab, an old link) goes to the browsable library
// tab, keeping the rest of the address.
export function OpenLibraryTab() {
  const [searchParams] = useSearchParams();
  const next = new URLSearchParams(searchParams);
  next.set("tab", "library");
  return <Navigate to={{ search: `?${next}` }} replace />;
}
export default function LibraryRecommended(props: ComponentProps<typeof Original>) {
  return usePreviewMode().active ? <OpenLibraryTab /> : <Original {...props} />;
}
