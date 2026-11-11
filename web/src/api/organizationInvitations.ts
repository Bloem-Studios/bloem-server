import type { components } from "@/api/v2/schema";

// Organization invitation wire types for the Bloem administration API. The
// organization routes answer with Silo's /api/v2 AdminInvitation.
export type Invitation = components["schemas"]["AdminInvitation"];
export type InvitationStatus = Invitation["status"];

export interface CreateInvitationRequest {
  email: string;
  role?: string;
  access_group_id?: number | null;
  library_ids?: number[] | null;
  create_profile?: boolean;
  show_tour?: boolean;
  note?: string;
}

export interface SendInvitationResponse {
  invitation: Invitation;
  email_sent: boolean;
  /** Only readable in this response — the server stores just the token hash. */
  claim_url?: string;
}
