import { useEffect, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@/api/client";
import type {
  Invitation,
  CreateInvitationRequest,
  SendInvitationResponse,
} from "@/api/organizationInvitations";
import { adminKeys } from "../keys";
import { toast } from "sonner";
import {
  adminV2Api,
  adminV2QueryKey,
  captureAdminRequestContext,
  type AdminRequestContext,
} from "@/api/adminV2Client";
import type { AdminContextSummary } from "@/api/bloemTypes";

const ADMIN_STALE_TIME = 30_000;

function invitationsKey(context?: AdminContextSummary | null) {
  return context?.scope === "organization"
    ? adminV2QueryKey(context.key, "organization", "invitations")
    : adminKeys.invitations();
}

export function useAdminInvitations(context?: AdminContextSummary | null) {
  const authority = context ? captureAdminRequestContext(context.key) : null;
  return useQuery({
    queryKey: invitationsKey(context),
    queryFn: () =>
      context?.scope === "organization"
        ? adminV2Api<{ invitations: Invitation[] }>(
            "/organization/invitations",
            {},
            "safe",
            authority,
          ).then((data) => data.invitations ?? [])
        : api<Invitation[]>("/admin/invitations").then((d) => d ?? []),
    staleTime: ADMIN_STALE_TIME,
  });
}

function isInvitationAuthorityActive(
  context: AdminContextSummary | null | undefined,
  authority: AdminRequestContext | null,
) {
  if (context?.scope !== "organization") return true;
  const current = captureAdminRequestContext(context.key);
  return Boolean(authority && current?.generation === authority.generation);
}

// Claim responses are a local, one-time handoff. Even a reset mutation retains
// its data/variables/callbacks in MutationCache, so these writes never use it.
function useInvitationHandoff<T>(
  context: AdminContextSummary | null | undefined,
  authority: AdminRequestContext | null,
  request: (variables: T) => Promise<SendInvitationResponse>,
  failureMessage: string,
) {
  const client = useQueryClient();
  const busy = useRef(false);
  const mounted = useRef(false);
  const [isPending, setPending] = useState(false);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  async function send(variables: T): Promise<SendInvitationResponse | undefined> {
    if (busy.current || !mounted.current || !isInvitationAuthorityActive(context, authority))
      return;
    busy.current = true;
    setPending(true);
    try {
      let response: SendInvitationResponse;
      try {
        response = await request(variables);
      } finally {
        // A lost response can follow a committed write. Keep the action busy
        // through readback, without ever rebinding it to a replacement context.
        if (isInvitationAuthorityActive(context, authority)) {
          await client.invalidateQueries({ queryKey: invitationsKey(context) });
        }
      }
      if (mounted.current && isInvitationAuthorityActive(context, authority)) return response;
    } catch (error) {
      if (mounted.current && isInvitationAuthorityActive(context, authority)) {
        toast.error(error instanceof Error ? error.message : failureMessage);
      }
    } finally {
      busy.current = false;
      if (mounted.current) setPending(false);
    }
  }

  return { send, isPending };
}

export function useCreateInvitation(context?: AdminContextSummary | null) {
  const authority = context ? captureAdminRequestContext(context.key) : null;
  return useInvitationHandoff(
    context,
    authority,
    async (body: CreateInvitationRequest) => {
      if (context?.scope === "organization") {
        const { role: _legacyRole, ...organizationBody } = body;
        const data = await adminV2Api<{
          invitation: Invitation;
          claim_token?: string;
        }>(
          "/organization/invitations",
          {
            method: "POST",
            body: JSON.stringify({
              ...organizationBody,
              expected_revision: context.policyRevision,
            }),
          },
          "none",
          authority,
        );
        return {
          invitation: data.invitation,
          email_sent: false,
          claim_url: data.claim_token
            ? `${window.location.origin}/invite/${encodeURIComponent(data.claim_token)}`
            : undefined,
        };
      }
      return api<SendInvitationResponse>(
        "/admin/invitations",
        {
          method: "POST",
          body: JSON.stringify(body),
        },
        "none",
      );
    },
    "Could not confirm invitation creation",
  );
}

export function useResendInvitation(context?: AdminContextSummary | null) {
  const authority = context ? captureAdminRequestContext(context.key) : null;
  return useInvitationHandoff(
    context,
    authority,
    async (id: number) => {
      if (context?.scope === "organization") {
        const data = await adminV2Api<{ invitation: Invitation; claim_token: string }>(
          `/organization/invitations/${id}/resend`,
          { method: "POST", body: JSON.stringify({ expected_revision: context.policyRevision }) },
          "none",
          authority,
        );
        return {
          invitation: data.invitation,
          email_sent: false,
          claim_url: `${window.location.origin}/invite/${encodeURIComponent(data.claim_token)}`,
        };
      }
      return api<SendInvitationResponse>(
        `/admin/invitations/${id}/resend`,
        { method: "POST" },
        "none",
      );
    },
    "Failed to resend invitation",
  );
}

export function useRevokeInvitation(context?: AdminContextSummary | null) {
  const queryClient = useQueryClient();
  const authority = context ? captureAdminRequestContext(context.key) : null;
  return useMutation({
    networkMode: "always",
    retry: false,
    mutationFn: (id: number) =>
      context?.scope === "organization"
        ? adminV2Api(
            `/organization/invitations/${id}`,
            {
              method: "DELETE",
              body: JSON.stringify({ expected_revision: context.policyRevision }),
            },
            "none",
            authority,
          )
        : api(`/admin/invitations/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Invitation revoked");
      queryClient.invalidateQueries({ queryKey: invitationsKey(context) });
    },
    onError: (err) => {
      void queryClient.invalidateQueries({ queryKey: invitationsKey(context) });
      toast.error(err instanceof Error ? err.message : "Failed to revoke invitation");
    },
  });
}
