import { nativeApi } from "@/api/bloemClient";
import { captureProfileRequestContext, captureSessionIdentity } from "@/api/client";

export interface ListConnection {
  installation_id: number;
  origin: string;
  can_create_server: boolean;
}
export interface ListCapability {
  available: boolean;
  connections: ListConnection[];
}
export interface ListPreview {
  id: string;
  status: string;
  name: string;
  kind: string;
  count: number;
  expires_at: string;
  failure: string;
}
export interface ListBinding {
  id: string;
  target_id: string;
  source_id: string;
  scope: "personal" | "server";
  status: string;
  generation: number;
  last_success: string | null;
  matched: number;
  unmatched: { entry: { title: string }; reason: string }[];
  failure: string;
  requests: {
    pending: number;
    blocked: number;
    delivered: number;
    cancelled: number;
    quota: number;
  };
}
export interface ListCreate {
  preview_id: string;
  scope: "personal" | "server";
  name: string;
  is_shared: boolean;
  library_ids?: number[];
}
const prefix = "/list-collections";
export const listCollectionsApi = {
  capabilities: () => nativeApi<ListCapability>(`${prefix}/capabilities`),
  preview: (installation_id: number, url: string) =>
    nativeApi<{ job_id: string }>(`${prefix}/previews`, {
      method: "POST",
      body: JSON.stringify({ installation_id, url }),
    }),
  getPreview: (id: string) =>
    nativeApi<ListPreview>(`${prefix}/previews/${encodeURIComponent(id)}`),
  create: (input: ListCreate) =>
    nativeApi<ListBinding>(`${prefix}/bindings`, { method: "POST", body: JSON.stringify(input) }),
  get: (id: string) => nativeApi<ListBinding>(`${prefix}/bindings/${encodeURIComponent(id)}`),
  refresh: (id: string) =>
    nativeApi<{ job_id: string }>(`${prefix}/bindings/${encodeURIComponent(id)}/refresh`, {
      method: "POST",
    }),
  disconnect: (id: string) =>
    nativeApi<{ status: string }>(`${prefix}/bindings/${encodeURIComponent(id)}/disconnect`, {
      method: "POST",
    }),
};
export function listActorKey() {
  const session = captureSessionIdentity();
  const profile = captureProfileRequestContext();
  return [
    session.serverOrigin,
    session.authContextVersion,
    profile?.profileId,
    profile?.profileTokenGeneration,
  ];
}
