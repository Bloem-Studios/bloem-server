import "@testing-library/jest-dom";

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
