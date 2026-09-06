import { v2 } from "@/api/v2/request";
import { requireNotificationAuthority } from "@/api/v2/notifications";
import { useRef } from "react";
import { testNotificationDestination } from "@/api/v2/notificationDestinationTests";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, captureProfileRequestContext, StaleApiRequestContextError } from "@/api/client";
import {
  notificationCapabilities,
  notificationScope,
  captureNotificationAuthority,
} from "@/api/v2/notifications";
import type { NotificationWebhook, NotificationWebhookInput } from "@/api/types";
import {
  listNotificationWebPushSubscriptions,
  listNotificationWebhooks,
} from "@/api/v2/notificationDestinations";
import { notificationKeys } from "./keys";
import { toast } from "sonner";

export function useNotificationCapability() {
  const context = captureProfileRequestContext();
  return useQuery({
    queryKey: [...notificationKeys.capability(), notificationScope(context)],
    queryFn: () => notificationCapabilities(context ?? captureNotificationAuthority()),
    enabled: context !== null,
    retry: false,
    staleTime: 5 * 60_000,
  });
}

export function useNotificationWebhooks(enabled = true) {
  const context = captureProfileRequestContext();
  return useQuery({
    queryKey: [...notificationKeys.webhooks(), notificationScope(context)],
    queryFn: () => listNotificationWebhooks(context ?? captureNotificationAuthority()),
    enabled: enabled && context !== null,
    retry: false,
  });
}

export function useCreateNotificationWebhook() {
  const queryClient = useQueryClient();
  const context = captureProfileRequestContext();
  const inFlight = useRef(false);
  return useMutation({
    retry: false,
    mutationFn: async (input: NotificationWebhookInput) => {
      if (!context) throw new StaleApiRequestContextError();
      requireNotificationAuthority(context);
      if (inFlight.current) throw new Error("Destination creation is already in progress.");
      if (!input.name || !input.url) throw new Error("A name and webhook URL are required.");
      inFlight.current = true;
      try {
        const result = await v2("POST /api/v2/notifications/webhooks", {
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
      void queryClient.invalidateQueries({ queryKey: notificationKeys.webhooks() });
    },
  });
}

export function useUpdateNotificationWebhook() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...input }: NotificationWebhookInput & { id: string }) =>
      api<NotificationWebhook>(`/notifications/webhooks/${id}`, {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: notificationKeys.webhooks() });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to update webhook");
    },
  });
}

export function useDeleteNotificationWebhook() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api(`/notifications/webhooks/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      toast.success("Webhook deleted");
      void queryClient.invalidateQueries({ queryKey: notificationKeys.webhooks() });
    },
    onError: () => {
      toast.error("Failed to delete webhook");
    },
  });
}

export function useTestNotificationWebhook() {
  const context = captureProfileRequestContext();
  const inFlight = useRef(false);
  return useMutation({
    retry: false,
    mutationFn: async (id: string) => {
      if (!context) throw new StaleApiRequestContextError();
      if (inFlight.current) throw new Error("A test delivery is already in progress.");
      inFlight.current = true;
      try {
        return await testNotificationDestination("webhook", id, context);
      } finally {
        inFlight.current = false;
      }
    },
  });
}

export function useRotateNotificationWebhookSecret() {
  return useMutation({
    mutationFn: (id: string) =>
      api<{ signing_secret: string }>(`/notifications/webhooks/${id}/rotate-secret`, {
        method: "POST",
      }),
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Failed to rotate signing secret");
    },
  });
}

export function useWebPushSubscriptions(enabled = true) {
  const context = captureProfileRequestContext();
  return useQuery({
    queryKey: [...notificationKeys.webPushSubscriptions(), notificationScope(context)],
    queryFn: () => listNotificationWebPushSubscriptions(context ?? captureNotificationAuthority()),
    enabled: enabled && context !== null,
    retry: false,
  });
}

export function useDeleteWebPushSubscription() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api(`/notifications/web-push/subscriptions/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: notificationKeys.webPushSubscriptions() });
    },
    onError: () => {
      toast.error("Failed to remove push subscription");
    },
  });
}
