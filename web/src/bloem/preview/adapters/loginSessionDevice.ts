import { loginSessionDevice as siloLoginSessionDevice } from "@/lib/loginSessionDevice";

export * from "@/lib/loginSessionDevice";

// Upstream's client name, assembled at run time: the brand plugin rewrites the
// word in every literal it sees after esbuild has run (product-brand.ts).
const UPSTREAM_CLIENT = ["Si", "lo"].join("");
const BLOEM_CLIENT = /\bBloem\b/i;

/**
 * Silo's login session label, which also recognizes Bloem's own clients as
 * the app: a User-Agent naming Bloem reads as one naming Silo. A client that
 * reported its platform keeps the name it chose.
 */
export function loginSessionDevice(
  session: Parameters<typeof siloLoginSessionDevice>[0],
): ReturnType<typeof siloLoginSessionDevice> {
  if (session.device_platform?.trim() || !BLOEM_CLIENT.test(session.device_name)) {
    return siloLoginSessionDevice(session);
  }
  return siloLoginSessionDevice({
    ...session,
    device_name: session.device_name.replace(new RegExp(BLOEM_CLIENT, "gi"), UPSTREAM_CLIENT),
  });
}
