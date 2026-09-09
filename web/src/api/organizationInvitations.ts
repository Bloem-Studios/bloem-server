// Organization invitation wire types for the Bloem administration API.
export type InvitationStatus = "pending" | "accepted" | "expired" | "revoked";

export interface Invitation {
  id: number;
  email: string;
  role: string;
  access_group_id?: number;
  library_ids?: number[];
  create_profile: boolean;
  show_tour: boolean;
  note?: string;
  invited_by: number;
  invited_by_name?: string;
  status: InvitationStatus;
  expires_at: string;
  accepted_at?: string;
  accepted_user_id?: number;
  created_at: string;
}

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
