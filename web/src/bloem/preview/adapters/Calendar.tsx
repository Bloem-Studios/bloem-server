import type { ReactNode } from "react";
import { Navigate, useSearchParams } from "react-router";
import Calendar from "@/pages/Calendar";
import { usePreviewMode } from "../usePreviewMode";
// A guest follows no shows, so the calendar's default Following view is always
// empty in a preview ("Nothing upcoming from shows you follow"). A preview
// opens on All instead; a filter already in the address stays as it is.
export function PreviewCalendarDefault({ children }: { children: ReactNode }) {
  const [searchParams] = useSearchParams();
  const preview = usePreviewMode().active;
  if (preview && !searchParams.get("filter")) {
    const next = new URLSearchParams(searchParams);
    next.set("filter", "everything");
    return <Navigate to={{ search: `?${next}` }} replace />;
  }
  return children;
}
export default function CalendarWithPreview() {
  return (
    <PreviewCalendarDefault>
      <Calendar />
    </PreviewCalendarDefault>
  );
}
