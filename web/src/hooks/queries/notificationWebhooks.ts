import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, captureProfileRequestContext } from "@/api/client";
import {
  notificationCapabilities,
  notificationScope,
  captureNotificationAuthority,
} from "@/api/v2/notifications";
import type {
  NotificationWebhook,
  NotificationWebhookInput,
  NotificationWebhookTestResult,
} from "@/api/types";
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
  return useMutation({
    mutationFn: (input: NotificationWebhookInput) =>
      api<NotificationWebhook>("/notifications/webhooks", {
        method: "POST",
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
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
  return useMutation({
    mutationFn: (id: string) =>
      api<NotificationWebhookTestResult>(`/notifications/webhooks/${id}/test`, {
        method: "POST",
      }),
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
