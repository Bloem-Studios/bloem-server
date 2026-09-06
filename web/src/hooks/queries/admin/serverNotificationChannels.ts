import { v2 } from "@/api/v2/request";
import { requireNotificationAuthority } from "@/api/v2/notifications";
import { useRef } from "react";
import { testNotificationDestination } from "@/api/v2/notificationDestinationTests";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, captureProfileRequestContext, StaleApiRequestContextError } from "@/api/client";
import type { ServerNotificationChannel, ServerNotificationChannelInput } from "@/api/types";
import { listNotificationServerChannels } from "@/api/v2/notificationDestinations";
import { captureNotificationAuthority, notificationScope } from "@/api/v2/notifications";
import { adminKeys } from "../keys";
import { toast } from "sonner";

export function useServerNotificationChannels() {
  const context = captureProfileRequestContext();
  return useQuery({
    queryKey: [...adminKeys.serverNotificationChannels(), notificationScope(context)],
    queryFn: () => listNotificationServerChannels(context ?? captureNotificationAuthority()),
    enabled: context !== null,
    retry: false,
  });
}

export function useCreateServerNotificationChannel() {
  const queryClient = useQueryClient();
  const context = captureProfileRequestContext();
  const inFlight = useRef(false);
  return useMutation({
    retry: false,
    mutationFn: async (input: ServerNotificationChannelInput) => {
      if (!context) throw new StaleApiRequestContextError();
      requireNotificationAuthority(context);
      if (inFlight.current) throw new Error("Destination creation is already in progress.");
      if (!input.name || !input.url) throw new Error("A name and webhook URL are required.");
      inFlight.current = true;
      try {
        const result = await v2("POST /api/v2/admin/notifications/server-channels", {
          body: { ...input, name: input.name, url: input.url },
          profileContext: context,
          retryAuthentication: false,
        });
        requireNotificationAuthority(context);
        return result;
      } finally {
        inFlight.current = false;
      }
    },
    onSuccess: () => {
      if (!context) return;
      requireNotificationAuthority(context);
      void queryClient.invalidateQueries({ queryKey: adminKeys.serverNotificationChannels() });
    },
  });
}

export function useUpdateServerNotificationChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...input }: ServerNotificationChannelInput & { id: string }) =>
      api<ServerNotificationChannel>(`/admin/notifications/server-channels/${id}`, {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: adminKeys.serverNotificationChannels() });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to update channel");
    },
  });
}

export function useDeleteServerNotificationChannel() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api(`/admin/notifications/server-channels/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Channel deleted");
      void queryClient.invalidateQueries({ queryKey: adminKeys.serverNotificationChannels() });
    },
    onError: () => {
      toast.error("Failed to delete channel");
    },
  });
}

export function useTestServerNotificationChannel() {
  const context = captureProfileRequestContext();
  const inFlight = useRef(false);
  return useMutation({
    retry: false,
    mutationFn: async (id: string) => {
      if (!context) throw new StaleApiRequestContextError();
      if (inFlight.current) throw new Error("A test delivery is already in progress.");
      inFlight.current = true;
      try {
        return await testNotificationDestination("server-channel", id, context);
      } finally {
        inFlight.current = false;
      }
    },
  });
}

export function useRotateServerNotificationChannelSecret() {
  return useMutation({
    mutationFn: (id: string) =>
      api<{ signing_secret: string }>(`/admin/notifications/server-channels/${id}/rotate-secret`, {
        method: "POST",
      }),
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to rotate signing secret");
    },
  });
}
