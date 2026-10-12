import { Input } from "@/components/ui/input";
import { audiobookConfigFields } from "./audiobookRequestFields";
import type { AudiobookConnectionInput } from "@/api/bloemAudiobookAcquisitions";
export function AudiobookRequestConfigForm({
  values,
  setValues,
  disabled,
}: {
  values: AudiobookConnectionInput["plugin_config"];
  setValues: (values: AudiobookConnectionInput["plugin_config"]) => void;
  disabled: boolean;
}) {
  return (
    <>
      {["Download clients", "Sources", "Matching and verification", "AI"].map((group) => (
        <details key={group} open={group === "Download clients"} className="rounded-lg border p-4">
          <summary className="cursor-pointer font-medium">{group}</summary>
          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            {audiobookConfigFields
              .filter((field) => field.group === group)
              .map((field) => {
                const value = values[field.key] ?? field.default;
                const update = (next: string | number | boolean | string[]) =>
                  setValues({ ...values, [field.key]: next });
                return (
                  <label key={field.key} className="grid content-start gap-2 text-sm">
                    <span className="font-medium">{field.label}</span>
                    {field.type === "boolean" ? (
                      <input
                        type="checkbox"
                        checked={value === true}
                        disabled={disabled}
                        onChange={(event) => update(event.target.checked)}
                        className="accent-primary h-4 w-4"
                      />
                    ) : field.enum ? (
                      <select
                        className="border-input h-9 min-w-0 rounded-md border bg-transparent px-3"
                        value={String(value)}
                        disabled={disabled}
                        onChange={(event) => update(event.target.value)}
                      >
                        {field.enum.map((option) => (
                          <option key={option} value={option}>
                            {option || "Not set"}
                          </option>
                        ))}
                      </select>
                    ) : field.type === "array" ? (
                      <textarea
                        className="border-input focus-visible:ring-ring min-h-20 w-full min-w-0 rounded-md border bg-transparent px-3 py-2 text-sm focus-visible:ring-2 focus-visible:outline-none"
                        value={Array.isArray(value) ? value.join("\n") : ""}
                        disabled={disabled}
                        maxLength={6000}
                        onChange={(event) => update(event.target.value.split("\n"))}
                      />
                    ) : (
                      <Input
                        type={
                          field.type === "integer" || field.type === "number" ? "number" : "text"
                        }
                        value={String(value)}
                        disabled={disabled}
                        min={field.minimum}
                        max={field.maximum}
                        maxLength={field.maxLength}
                        step={
                          field.type === "integer" ? 1 : field.type === "number" ? "any" : undefined
                        }
                        onChange={(event) =>
                          update(
                            field.type === "integer" || field.type === "number"
                              ? Number(event.target.value)
                              : event.target.value,
                          )
                        }
                      />
                    )}
                    {field.description && (
                      <span className="text-muted-foreground text-xs">
                        {field.description}
                        {field.type === "array" ? " One per line." : ""}
                      </span>
                    )}
                  </label>
                );
              })}
          </div>
        </details>
      ))}
    </>
  );
}
