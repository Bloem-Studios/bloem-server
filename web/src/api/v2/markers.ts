import type { FileMarkersResponse, MarkerSegment, SetMarkersRequest } from "@/api/types";
import { markerUpdateToV2 } from "@/player/marker-wire";
import { v2, type V2Result } from "./request";

type FileMarkers = V2Result<"GET /api/v2/markers/items/{item_id}">;

function markerSegmentFromV2(segment: FileMarkers["intro"]): MarkerSegment {
  return {
    start: segment.start_seconds ?? null,
    end: segment.end_seconds ?? null,
    source: segment.source ?? null,
    provider: segment.provider ?? null,
    confidence: segment.confidence ?? null,
    algorithm: segment.algorithm ?? null,
    detected_at: segment.detected_at ?? null,
  };
}

function fileMarkersFromV2(markers: FileMarkers): FileMarkersResponse {
  return {
    file_id: Number(markers.file_id),
    intro: markerSegmentFromV2(markers.intro),
    credits: markerSegmentFromV2(markers.credits),
    recap: markerSegmentFromV2(markers.recap),
    preview: markerSegmentFromV2(markers.preview),
  };
}

export async function getItemMarkers(itemId: string, signal?: AbortSignal) {
  return fileMarkersFromV2(
    await v2("GET /api/v2/markers/items/{item_id}", {
      path: { item_id: itemId },
      signal,
    }),
  );
}

export async function setItemMarkers(itemId: string, update: SetMarkersRequest) {
  return fileMarkersFromV2(
    await v2("PUT /api/v2/markers/items/{item_id}", {
      path: { item_id: itemId },
      body: markerUpdateToV2(update),
    }),
  );
}
