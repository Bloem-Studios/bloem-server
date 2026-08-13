import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import type { Invitation, CreateInvitationRequest, SendInvitationResponse } from "@/api/types";
import { adminKeys } from "../keys";
import { toast } from "sonner";
import { adminV2Api, adminV2QueryKey } from "@/api/adminV2Client";
import type { AdminContextSummary } from "@/api/types";

const ADMIN_STALE_TIME = 30_000;

function invitationsKey(context?: AdminContextSummary | null) {
  return context?.scope === "organization"
    ? adminV2QueryKey(context.key, "organization", "invitations")
    : adminKeys.invitations();
}

export function useAdminInvitations(context?: AdminContextSummary | null) {
  return useQuery({
    queryKey: invitationsKey(context),
    queryFn: () =>
      context?.scope === "organization"
        ? adminV2Api<{ invitations: Invitation[] }>("/organization/invitations").then(
            (data) => data.invitations ?? [],
          )
        : api<Invitation[]>("/admin/invitations").then((d) => d ?? []),
    staleTime: ADMIN_STALE_TIME,
  });
}

export function useCreateInvitation(context?: AdminContextSummary | null) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (body: CreateInvitationRequest) => {
      if (context?.scope === "organization") {
        const data = await adminV2Api<{
          invitation: Invitation;
          claim_token?: string;
        }>("/organization/invitations", {
          method: "POST",
          body: JSON.stringify({ ...body, expected_revision: context.policyRevision }),
        });
        return {
          invitation: data.invitation,
          email_sent: false,
          claim_url: data.claim_token
            ? `${window.location.origin}/invite/${encodeURIComponent(data.claim_token)}`
            : undefined,
        } satisfies SendInvitationResponse;
      }
      return api<SendInvitationResponse>("/admin/invitations", {
        method: "POST",
        body: JSON.stringify(body),
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: invitationsKey(context) });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to send invitation");
    },
  });
}

export function useResendInvitation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) =>
      api<SendInvitationResponse>(`/admin/invitations/${id}/resend`, { method: "POST" }),
    onSuccess: (data) => {
      if (data.email_sent) {
        toast.success("Invitation resent — the old link no longer works");
      }
      queryClient.invalidateQueries({ queryKey: adminKeys.invitations() });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to resend invitation");
    },
  });
}

export function useRevokeInvitation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => api(`/admin/invitations/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Invitation revoked");
      queryClient.invalidateQueries({ queryKey: adminKeys.invitations() });
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : "Failed to revoke invitation");
    },
  });
}
