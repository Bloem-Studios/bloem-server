import { Input } from "@/components/ui/input";
import { useBloemT, type BloemMessageKey } from "@/bloem/i18n";
import { audiobookConfigFields, type AudiobookConfigGroup } from "./audiobookRequestFields";
import type { AudiobookConnectionInput } from "@/api/bloemAudiobookAcquisitions";
const groups: [AudiobookConfigGroup, BloemMessageKey][] = [
  ["downloadClients", "bloem.admin.bookRequests.audiobookConfig.group.downloadClients"],
  ["sources", "bloem.admin.bookRequests.audiobookConfig.group.sources"],
  ["matching", "bloem.admin.bookRequests.audiobookConfig.group.matching"],
  ["ai", "bloem.admin.bookRequests.audiobookConfig.group.ai"],
];
export function AudiobookRequestConfigForm({
  values,
  setValues,
  disabled,
}: {
  values: AudiobookConnectionInput["plugin_config"];
  setValues: (values: AudiobookConnectionInput["plugin_config"]) => void;
  disabled: boolean;
}) {
  const bt = useBloemT();
  return (
    <>
      {groups.map(([group, heading]) => (
        <details key={group} open={group === "downloadClients"} className="rounded-lg border p-4">
          <summary className="cursor-pointer font-medium">{bt(heading)}</summary>
          <div className="mt-4 grid gap-4 sm:grid-cols-2">
            {audiobookConfigFields
              .filter((field) => field.group === group)
              .map((field) => {
                const value = values[field.key] ?? field.default;
                const update = (next: string | number | boolean | string[]) =>
                  setValues({ ...values, [field.key]: next });
                const description = field.description?.();
                return (
                  <label key={field.key} className="grid content-start gap-2 text-sm">
                    <span className="font-medium">{field.label()}</span>
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
                            {option || bt("bloem.admin.bookRequests.audiobookConfig.notSet")}
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
                    {description && (
                      <span className="text-muted-foreground text-xs">
                        {field.type === "array"
                          ? bt("bloem.admin.bookRequests.audiobookConfig.onePerLine", {
                              description,
                            })
                          : description}
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
