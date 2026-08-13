import { Building2, ShieldCheck } from "lucide-react";
import { useAdminContext } from "@/contexts/AdminContextProvider";

function authorityLabel(authority: "platform_admin" | "organization_admin") {
  return authority === "platform_admin" ? "Platform administrator" : "Organization administrator";
}

export default function AdminContextSwitcher({
  onSwitchSuccess,
}: {
  onSwitchSuccess?: () => void;
}) {
  const { active, available, switching, switchContext, failure } = useAdminContext();

  return (
    <div className="border-sidebar-border/70 bg-sidebar-accent/35 mx-3 mb-4 rounded-xl border p-3">
      <div className="flex items-start gap-2.5">
        <div className="text-primary bg-primary/10 mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg">
          {active?.scope === "organization" ? (
            <Building2 className="h-4 w-4" aria-hidden="true" />
          ) : (
            <ShieldCheck className="h-4 w-4" aria-hidden="true" />
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-sidebar-foreground truncate text-sm font-semibold">
            {active?.name ?? (switching ? "Loading context…" : "Choose context")}
          </div>
          {active ? (
            <>
              <div className="text-muted-foreground mt-0.5 text-[11px] capitalize">
                {active.scope} · {active.status}
              </div>
              <div className="text-muted-foreground text-[11px]">
                {authorityLabel(active.authority)}
              </div>
            </>
          ) : null}
        </div>
      </div>
      <label className="text-muted-foreground mt-3 block text-[10px] font-semibold tracking-[0.14em] uppercase">
        <span className="sr-only">Administrative context</span>
        <select
          aria-label="Administrative context"
          value={active?.key ?? ""}
          disabled={switching || available.length === 0}
          onChange={(event) =>
            void switchContext(
              event.target.value as (typeof available)[number]["key"],
              onSwitchSuccess,
            )
          }
          className="border-sidebar-border bg-sidebar text-sidebar-foreground focus:ring-ring/50 mt-1 h-10 w-full rounded-lg border px-2 text-xs font-medium normal-case focus:ring-2 focus:outline-none"
        >
          {!active ? <option value="">Select context</option> : null}
          {available.map((context) => (
            <option key={context.key} value={context.key}>
              {context.name}
            </option>
          ))}
        </select>
      </label>
      <div className="sr-only" role="status" aria-live="polite">
        {failure?.message ?? (active ? `Administrative context changed to ${active.name}` : "")}
      </div>
    </div>
  );
}
