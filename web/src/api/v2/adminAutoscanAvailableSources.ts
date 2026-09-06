import {
  isCapturedProfileAuthorityActive,
  StaleApiRequestContextError,
  type ProfileRequestContextSnapshot,
} from "@/api/client";
import type { AutoscanAvailableSource } from "@/api/types";
import { v2 } from "./request";

export async function readAdminAutoscanAvailableSources(
  profileContext: ProfileRequestContextSnapshot,
): Promise<AutoscanAvailableSource[]> {
  const rows: AutoscanAvailableSource[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  for (let page = 0; page < 100; page++) {
    if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
    const result = await v2("GET /api/v2/admin/autoscan/scan-source-plugins", {
      profileContext,
      query: { limit: 100, cursor },
    });
    if (!isCapturedProfileAuthorityActive(profileContext)) throw new StaleApiRequestContextError();
    rows.push(
      ...result.items.map(
        (row): AutoscanAvailableSource => ({
          ...row,
          descriptor: {
            ...row.descriptor,
            delivery_modes: row.descriptor.delivery_modes.filter(
              (mode): mode is "poll" | "webhook" => mode === "poll" || mode === "webhook",
            ),
            connection:
              row.descriptor.connection === "none" || row.descriptor.connection === "required"
                ? row.descriptor.connection
                : "optional",
            config_form: row.descriptor.config_form
              ? {
                  ...row.descriptor.config_form,
                  fields: row.descriptor.config_form.fields.map((field) => ({
                    ...field,
                    control:
                      field.control === "TEXTAREA" ||
                      field.control === "PASSWORD" ||
                      field.control === "NUMBER" ||
                      field.control === "SWITCH" ||
                      field.control === "SELECT" ||
                      field.control === "MULTI_SELECT"
                        ? field.control
                        : "TEXT",
                    required: field.required ?? false,
                    secret: field.secret ?? false,
                    multiline: field.multiline ?? false,
                  })),
                  sections: row.descriptor.config_form.sections?.map((section) => ({
                    ...section,
                    collapsible: section.collapsible ?? false,
                    collapsed_default: section.collapsed_default ?? false,
                  })),
                }
              : undefined,
          },
        }),
      ),
    );
    if (!result.page || typeof result.page.has_more !== "boolean")
      throw new Error("Invalid autoscan source descriptor page.");
    if (!result.page.has_more) return rows;
    const next = result.page.next_cursor;
    if (!next || seen.has(next))
      throw new Error("Invalid autoscan source descriptor continuation.");
    seen.add(next);
    cursor = next;
  }
  throw new Error("Too many autoscan source descriptors to display.");
}
