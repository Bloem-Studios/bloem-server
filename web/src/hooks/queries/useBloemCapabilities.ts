import { useQuery } from "@tanstack/react-query";

export interface BloemCapabilities {
  feature_tokens: string[];
  features: {
    direct_profile_login?: boolean;
    shared_device_pairing?: boolean;
    delegated_admin_roles?: boolean;
  };
}

export function useBloemCapabilities(enabled = true) {
  return useQuery({
    queryKey: ["bloem-capabilities", window.location.origin],
    enabled,
    queryFn: async ({ signal }): Promise<BloemCapabilities> => {
      const response = await fetch("/api/bloem/v1/capabilities", {
        signal,
        headers: { Accept: "application/json" },
      });
      if (!response.ok) throw new Error("Could not check this server’s supported Bloem features.");
      return response.json();
    },
    staleTime: 60_000,
    retry: false,
  });
}
