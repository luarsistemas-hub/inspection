"use client";

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";

/** Theme preference supported by all Inspection products. */
export type ThemePreference = "system" | "light" | "dark";
export type ResolvedTheme = Exclude<ThemePreference, "system">;
export type ThemeContextValue = { preference: ThemePreference; resolvedTheme: ResolvedTheme; setPreference: (preference: ThemePreference) => void };
export const themeStorageKey = "inspection.theme";

const ThemeContext = createContext<ThemeContextValue | null>(null);
const isThemePreference = (value: unknown): value is ThemePreference => value === "system" || value === "light" || value === "dark";
const readPreference = (): ThemePreference => { try { const value = localStorage.getItem(themeStorageKey); return isThemePreference(value) ? value : "system"; } catch { return "system"; } };
const resolve = (preference: ThemePreference, systemIsDark: boolean): ResolvedTheme => preference === "system" ? systemIsDark ? "dark" : "light" : preference;

/** Lets product shells select the shared light, dark, or system color scheme. */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setStoredPreference] = useState<ThemePreference>("system");
  const [systemIsDark, setSystemIsDark] = useState(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => setSystemIsDark(media.matches);
    const onStorage = (event: StorageEvent) => {
      if (event.key === themeStorageKey || event.key === null) setStoredPreference(readPreference());
    };
    apply();
    const stored = readPreference();
    setStoredPreference(stored);
    setReady(true);
    media.addEventListener("change", apply);
    window.addEventListener("storage", onStorage);
    return () => { media.removeEventListener("change", apply); window.removeEventListener("storage", onStorage); };
  }, []);

  const resolvedTheme = resolve(preference, systemIsDark);
  useEffect(() => {
    if (!ready) return;
    document.documentElement.dataset.theme = resolvedTheme;
    const color = resolvedTheme === "dark" ? "#111522" : "#f5f7ff";
    document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute("content", color);
  }, [ready, resolvedTheme]);

  const setPreference = useCallback((next: ThemePreference) => {
    setStoredPreference(next);
    try { if (next === "system") localStorage.removeItem(themeStorageKey); else localStorage.setItem(themeStorageKey, next); } catch { /* The in-memory choice remains available. */ }
    const isDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
    const nextTheme = resolve(next, isDark);
    document.documentElement.dataset.theme = nextTheme;
    document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute("content", nextTheme === "dark" ? "#111522" : "#f5f7ff");
  }, []);

  return <ThemeContext.Provider value={{ preference, resolvedTheme, setPreference }}>{children}</ThemeContext.Provider>;
}

/** Reads and changes the persisted Inspection theme preference. */
export function useTheme(): ThemeContextValue {
  const theme = useContext(ThemeContext);
  if (!theme) throw new Error("useTheme must be used inside ThemeProvider");
  return theme;
}

/** Runs before first paint to avoid a flash when restoring a saved or system theme. */
export function ThemeScript() {
  const script = `(()=>{let p="system";try{const v=localStorage.getItem("${themeStorageKey}");if(v==="light"||v==="dark")p=v}catch{}const d=p==="dark"||(p==="system"&&matchMedia("(prefers-color-scheme: dark)").matches);document.documentElement.dataset.theme=d?"dark":"light";const m=document.querySelector('meta[name="theme-color"]');if(m)m.content=d?"#111522":"#f5f7ff"})();`;
  return <script dangerouslySetInnerHTML={{ __html: script }} />;
}

/** Compact accessible selector that follows the operating system by default. */
export function ThemeSelector({ className = "" }: { className?: string }) {
  const { preference, setPreference } = useTheme();
  return <label className={`inspection-theme-selector ${className}`.trim()}><span>Aparência</span><select aria-label="Aparência" value={preference} onChange={(event) => setPreference(event.target.value as ThemePreference)}><option value="system">Sistema</option><option value="light">Claro</option><option value="dark">Escuro</option></select></label>;
}
