import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  api,
  captureProfileRequestContext,
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import { v2 } from "@/api/v2/request";
import type {
  StreamNode,
  NodeCapabilities,
  NodeLastStats,
  CreateNodeRequest,
  UpdateNodeRequest,
  ReprobeNodeResult,
} from "@/api/types";
import { adminKeys } from "../keys";
import { usePageActivity } from "@/hooks/usePageActivity";
import { describeReprobeOutcome } from "@/pages/adminNodesPresentation";
import { toast } from "sonner";

const ADMIN_STALE_TIME = 30_000;

export async function fetchAdminNodes(
  profileContext = captureProfileRequestContext(),
): Promise<StreamNode[]> {
  if (!profileContext) throw new StaleApiRequestContextError();
  const nodes: StreamNode[] = [];
  let cursor: string | undefined;
  do {
    const page = await v2("GET /api/v2/admin/nodes", {
      profileContext,
      query: { limit: 200, cursor },
    });
    if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
    nodes.push(
      ...page.items.map((row) => ({
        ...row,
        capabilities: row.capabilities as NodeCapabilities | undefined,
        last_stats: row.last_stats as NodeLastStats | undefined,
      })),
    );
    cursor = page.page?.next_cursor;
  } while (cursor);
  return nodes;
}

/**
 * Polled on the node health cadence, because this row now carries live
 * readings rather than configuration.
 *
 * `staleTime` alone marks data old; it does not schedule anything. Without an
 * interval the GPU, disk and health columns froze at whatever they were when
 * the page mounted, refreshing only on focus, reconnect or a mutation — so an
 * operator watching a node saturate, a scratch volume fill, or a health check
 * start failing would see none of it. The server persists a fresh sample every
 * 30 seconds, so asking more often only costs requests.
 *
 * Gated on page activity: a backgrounded or frozen tab has nobody reading it,
 * and polling every admin tab a browser has open is how a small deployment
 * ends up serving its own dashboard.
 */
export function useAdminNodes() {
  const pageActivity = usePageActivity();
  const profileContext = captureProfileRequestContext();
  return useQuery({
    queryKey: [
      ...adminKeys.nodes(),
      profileContext?.serverOrigin,
      profileContext?.authContextVersion,
      profileContext?.profileId,
      profileContext?.profileTokenGeneration,
    ],
    enabled: profileContext !== null,
    queryFn: () => fetchAdminNodes(profileContext),
    staleTime: ADMIN_STALE_TIME,
    refetchInterval: pageActivity.canApplyRealtimeUpdates ? ADMIN_STALE_TIME : false,
  });
}

export function useCreateNode() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateNodeRequest) =>
      api("/admin/nodes", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      toast.success("Node created");
      queryClient.invalidateQueries({ queryKey: adminKeys.nodes() });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to save");
    },
  });
}

export function useUpdateNode() {
  const queryClient = useQueryClient();
  return useMutation({
    // The body is typed rather than a loose record so a null acceleration
    // override — the value that restores inheritance of the cluster-wide
    // setting — survives to the wire instead of being dropped as a typo.
    mutationFn: ({ id, body }: { id: StreamNode["id"]; body: UpdateNodeRequest }) =>
      api<StreamNode>(`/admin/nodes/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: adminKeys.nodes() });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to update node");
    },
  });
}

export function useDeleteNode() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: StreamNode["id"]) => api(`/admin/nodes/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Node deleted");
      queryClient.invalidateQueries({ queryKey: adminKeys.nodes() });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to delete node");
    },
  });
}

type NodeCommandIntent = { node: StreamNode; authority: ProfileRequestContextSnapshot };
function useNodeObservationCommand(action: "check" | "reprobe") {
  const queryClient = useQueryClient();
  const renderedAuthority = captureProfileRequestContext();
  const mutation = useMutation({
    retry: false,
    mutationFn: async (intent: NodeCommandIntent) => {
      if (!isCapturedProfileAuthorityActive(intent.authority))
        throw new StaleApiRequestContextError();
      const options = {
        path: { id: String(intent.node.id) },
        profileContext: intent.authority,
        retryAuthentication: false,
      };
      const result =
        action === "check"
          ? await v2("POST /api/v2/admin/nodes/{id}/check", options)
          : await v2("POST /api/v2/admin/nodes/{id}/reprobe", options);
      if (!isCapturedProfileAuthorityActive(intent.authority))
        throw new StaleApiRequestContextError();
      if ("node_id" in result && result.node_id !== String(intent.node.id))
        throw new Error("Unexpected node observation");
      return result;
    },
    onSuccess: (result, intent) => {
      if (!isCapturedProfileAuthorityActive(intent.authority)) return;
      if ("healthy" in result) {
        toast.success(
          result.healthy ? `${intent.node.name} is healthy` : `${intent.node.name} is unhealthy`,
        );
        if (!result.health_persisted)
          toast.error("Health was observed but could not be stored. Refresh node state.");
      } else {
        const outcome = describeReprobeOutcome(intent.node, {
          ...result,
          node_id: Number(result.node_id),
        } as ReprobeNodeResult);
        if (outcome.ok) toast.success(outcome.message);
        else toast.error(outcome.message);
      }
    },
    onError: (_error, intent) => {
      if (isCapturedProfileAuthorityActive(intent.authority))
        toast.error(
          "Node command could not be confirmed. Inspect node state before another explicit command.",
        );
    },
    onSettled: (_result, _error, intent) => {
      if (isCapturedProfileAuthorityActive(intent.authority))
        queryClient.invalidateQueries({ queryKey: adminKeys.nodes() });
    },
  });
  return {
    ...mutation,
    variables: mutation.variables?.node,
    mutate: (node: StreamNode) => {
      if (!renderedAuthority || !isCapturedProfileAuthorityActive(renderedAuthority)) return;
      mutation.mutate({ node: structuredClone(node), authority: renderedAuthority });
    },
  };
}
export function useCheckNodeHealth() {
  return useNodeObservationCommand("check");
}
export function useReprobeNode() {
  return useNodeObservationCommand("reprobe");
}

export function useToggleNode() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (node: StreamNode) =>
      api<StreamNode>(`/admin/nodes/${node.id}`, {
        method: "PUT",
        body: JSON.stringify({ enabled: !node.enabled }),
      }),
    onSuccess: (updated) => {
      toast.success(`${updated.name} ${updated.enabled ? "enabled" : "disabled"}`);
      queryClient.invalidateQueries({ queryKey: adminKeys.nodes() });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to update node");
    },
  });
}
