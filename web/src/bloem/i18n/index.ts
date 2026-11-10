// Bloem's message API, alongside Silo's `@/i18n` (see docs/bloem/web-messages.md).
// Components use `useBloemT()`; plain code uses `bloemT()`.
export type { BloemMessageKey } from "@/bloem/locales/keys";
export { loadBloemAdminEnglishForTests } from "./catalog";
export {
  bloemEnglishText,
  bloemT,
  useBloemAdminMessages,
  useBloemT,
  type BloemTranslate,
} from "./format";
