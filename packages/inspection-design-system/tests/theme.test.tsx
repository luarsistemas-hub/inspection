import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ThemeProvider, ThemeSelector, themeStorageKey, useTheme } from "../src/index.js";

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

const originalMatchMedia = window.matchMedia;
const originalLocalStorage = window.localStorage;
let storedValues = new Map<string, string>();
const testStorage: Storage = {
  get length() { return storedValues.size; },
  clear: () => storedValues.clear(),
  getItem: (key) => storedValues.get(key) ?? null,
  key: (index) => [...storedValues.keys()][index] ?? null,
  removeItem: (key) => { storedValues.delete(key); },
  setItem: (key, value) => { storedValues.set(key, String(value)); }
};
beforeEach(() => {
  storedValues = new Map();
  Object.defineProperty(window, "localStorage", { configurable: true, value: testStorage });
});
afterEach(() => {
  storedValues.clear();
  document.documentElement.removeAttribute("data-theme");
  if (originalMatchMedia) window.matchMedia = originalMatchMedia;
  else Reflect.deleteProperty(window, "matchMedia");
  if (originalLocalStorage) Object.defineProperty(window, "localStorage", { configurable: true, value: originalLocalStorage });
  else Reflect.deleteProperty(window, "localStorage");
});

describe("shared theme preference", () => {
  it("defaults to system, persists an explicit choice, and resolves its color scheme", async () => {
    window.matchMedia = (() => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined })) as typeof window.matchMedia;
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    let resolved = "";
    function ThemeProbe() {
      const theme = useTheme();
      resolved = theme.resolvedTheme;
      return <ThemeSelector />;
    }

    await act(async () => root.render(<ThemeProvider><ThemeProbe /></ThemeProvider>));
    expect(container.querySelector("select")?.value).toBe("system");
    expect(resolved).toBe("light");
    await act(async () => {
      const selector = container.querySelector("select")!;
      selector.value = "dark";
      selector.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(window.localStorage.getItem(themeStorageKey)).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(resolved).toBe("dark");

    await act(async () => root.unmount());
    container.remove();
  });

  it("ignores invalid stored preferences and the early script restores the system choice", async () => {
    window.localStorage.setItem(themeStorageKey, "ultraviolet");
    window.matchMedia = (() => ({ matches: true, addEventListener: () => undefined, removeEventListener: () => undefined })) as typeof window.matchMedia;
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<ThemeProvider><ThemeSelector /></ThemeProvider>));
    expect(container.querySelector("select")?.value).toBe("system");
    expect(document.documentElement.dataset.theme).toBe("dark");
    await act(async () => root.unmount());
    container.remove();
  });

  it("follows system changes and synchronized storage updates", async () => {
    let systemIsDark = false;
    const listeners = new Set<(event: MediaQueryListEvent) => void>();
    window.matchMedia = (() => ({
      get matches() { return systemIsDark; },
      addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => { listeners.add(listener); },
      removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => { listeners.delete(listener); }
    })) as typeof window.matchMedia;
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<ThemeProvider><ThemeSelector /></ThemeProvider>));
    expect(document.documentElement.dataset.theme).toBe("light");

    systemIsDark = true;
    await act(async () => listeners.forEach((listener) => listener({ matches: true } as MediaQueryListEvent)));
    expect(document.documentElement.dataset.theme).toBe("dark");

    storedValues.set(themeStorageKey, "light");
    await act(async () => window.dispatchEvent(new StorageEvent("storage", { key: themeStorageKey })));
    expect(container.querySelector("select")?.value).toBe("light");
    expect(document.documentElement.dataset.theme).toBe("light");
    await act(async () => root.unmount());
    container.remove();
  });

  it("keeps theme switching available when browser storage is unavailable", async () => {
    Object.defineProperty(window, "localStorage", { configurable: true, value: {
      getItem() { throw new DOMException("Storage is blocked", "SecurityError"); },
      setItem() { throw new DOMException("Storage is blocked", "SecurityError"); },
      removeItem() { throw new DOMException("Storage is blocked", "SecurityError"); }
    } });
    window.matchMedia = (() => ({ matches: false, addEventListener: () => undefined, removeEventListener: () => undefined })) as typeof window.matchMedia;
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<ThemeProvider><ThemeSelector /></ThemeProvider>));
    const selector = container.querySelector("select")!;
    await act(async () => {
      selector.value = "dark";
      selector.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(selector.value).toBe("dark");
    await act(async () => root.unmount());
    container.remove();
  });
});
