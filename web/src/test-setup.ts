import "@testing-library/jest-dom";
import { vi } from "vitest";

import { loadAdminEnglishForTests } from "@/i18n";
import { loadBloemAdminEnglishForTests } from "@/bloem/i18n";

// Tests render admin pages and call admin helpers without the admin layout
// that would load their text.
await loadAdminEnglishForTests();
// Bloem's admin text, likewise (src/bloem/i18n).
await loadBloemAdminEnglishForTests();

// Components render inside the app's message provider. Tests render them alone,
// so every render and renderHook gets the English one, which formats from the
// generated English catalog and throws on a formatting error. A test's own
// wrapper still applies, inside this one, and rerender keeps both.
vi.mock("@testing-library/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@testing-library/react")>();
  const { createElement } = await import("react");
  const { AppIntlProvider, createAppIntl } = await import("@/i18n");
  const english = createAppIntl("en", {});
  type Wrapper = React.JSXElementConstructor<{ children: React.ReactNode }>;

  function withEnglish(wrapper: Wrapper | undefined): Wrapper {
    return function EnglishIntl({ children }) {
      return createElement(
        AppIntlProvider,
        { intl: english },
        wrapper ? createElement(wrapper, null, children) : children,
      );
    };
  }

  const render = ((ui: React.ReactNode, options?: { wrapper?: Wrapper }) =>
    actual.render(ui, {
      ...options,
      wrapper: withEnglish(options?.wrapper),
    })) as typeof actual.render;
  const renderHook = ((callback: (props: unknown) => unknown, options?: { wrapper?: Wrapper }) =>
    actual.renderHook(callback, {
      ...options,
      wrapper: withEnglish(options?.wrapper),
    })) as typeof actual.renderHook;

  return { ...actual, render, renderHook };
});

function createMemoryStorage(): Storage {
  const values = new Map<string, string>();

  return {
    get length() {
      return values.size;
    },
    clear() {
      values.clear();
    },
    getItem(key: string) {
      return values.get(key) ?? null;
    },
    key(index: number) {
      return Array.from(values.keys())[index] ?? null;
    },
    removeItem(key: string) {
      values.delete(key);
    },
    setItem(key: string, value: string) {
      values.set(key, String(value));
    },
  };
}

// Node 26 exposes an experimental global localStorage whose value is undefined
// unless the process receives --localstorage-file. That property can shadow
// jsdom's implementation, so replace it with deterministic per-file storage in
// browser tests. Node-environment tests have no window and remain untouched.
if (typeof window !== "undefined") {
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    value: createMemoryStorage(),
  });
}
