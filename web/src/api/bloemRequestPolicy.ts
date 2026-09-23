// Bloem request retry policy. Kept in a leaf module (no import of client.ts)
// so the Silo-owned client.ts only needs a single import to use it.

export type RequestPolicy = "safe" | "none" | "idempotentLifecycle";

const MAX_RETRYABLE_FAILURES = 2;

export function resolveRequestPolicy(options: RequestInit, policy?: RequestPolicy): RequestPolicy {
  if (policy) return policy;
  const method = (options.method ?? "GET").toUpperCase();
  return method === "GET" || method === "HEAD" || method === "OPTIONS" ? "safe" : "none";
}

function canRetryResponse(policy: RequestPolicy, failures: number, status: number): boolean {
  return (
    policy !== "none" && failures < MAX_RETRYABLE_FAILURES && (status === 429 || status === 503)
  );
}

function canRetryTransport(policy: RequestPolicy, failures: number, error: unknown): boolean {
  return (
    policy !== "none" &&
    failures < MAX_RETRYABLE_FAILURES &&
    !(error instanceof DOMException && error.name === "AbortError")
  );
}

/**
 * Returns a fetch that retries transport failures and 429/503 responses per
 * `policy`, sharing one failure budget across every call made through it (the
 * original request and its post-refresh retry). `afterAttempt` runs after each
 * completed fetch, before a retry decision, so a stale request context can
 * abort instead of being replayed. Policy "none" performs exactly one fetch.
 */
export function createPolicyFetch(
  policy: RequestPolicy,
  afterAttempt: () => void,
): (url: string, init: RequestInit) => Promise<Response> {
  let failures = 0;
  return async (url, init) => {
    for (;;) {
      let res: Response;
      try {
        res = await fetch(url, init);
      } catch (error) {
        if (!canRetryTransport(policy, failures, error)) throw error;
        failures += 1;
        continue;
      }
      afterAttempt();
      if (canRetryResponse(policy, failures, res.status)) {
        failures += 1;
        continue;
      }
      return res;
    }
  };
}
