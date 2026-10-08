import type { SVGProps } from "react";

export type IconName = "check" | "info" | "warning" | "error" | "menu" | "close" | "more-horizontal" | "calendar" | "arrow-right" | "chevron-down" | "camera" | "map-pin" | "layout-dashboard" | "building-2" | "users" | "clipboard-check" | "settings" | "sparkles" | "shield-check" | "history" | "chart-no-axes-combined" | "settings-2" | "search" | "arrow-up-right" | "log-out" | "moon" | "sun" | "monitor" | "eye";
export type IconProps = SVGProps<SVGSVGElement> & { name: IconName; title?: string; size?: number };

const paths: Record<IconName, string> = {
  check: "M5 12l4 4L19 6",
  info: "M12 8h.01M11 12h1v4h1M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z",
  warning: "M12 3 2 21h20L12 3Zm0 6v5m0 3h.01",
  error: "M12 9v4m0 4h.01M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z",
  menu: "M4 7h16M4 12h16M4 17h16",
  close: "m18 6-12 12M6 6l12 12",
  "more-horizontal": "M5 12h.01M12 12h.01M19 12h.01",
  calendar: "M8 2v4m8-4v4M3 10h18M5 4h14a2 2 0 0 1 2 2v14H3V6a2 2 0 0 1 2-2Z",
  "arrow-right": "M5 12h14m-5-5 5 5-5 5",
  "chevron-down": "m6 9 6 6 6-6",
  camera: "M4 7h4l1.5-2h5L16 7h4v12H4V7Zm8 3a3 3 0 1 0 0 6 3 3 0 0 0 0-6Z",
  "map-pin": "M20 10c0 5-8 12-8 12S4 15 4 10a8 8 0 1 1 16 0ZM12 10h.01",
  "layout-dashboard": "M3 3h8v8H3zM13 3h8v5h-8zM13 10h8v11h-8zM3 13h8v8H3z",
  "building-2": "M6 22V3h12v19M3 22h18M9 7h1m4 0h1M9 11h1m4 0h1M9 15h1m4 0h1M10 22v-3h4v3",
  users: "M16 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2m12-11a4 4 0 0 1 0 8m4 3v-2a4 4 0 0 0-3-3.87M10 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8Z",
  "clipboard-check": "M9 4h6m-6-2h6a2 2 0 0 1 2 2v2H7V4a2 2 0 0 1 2-2Zm9 2h2v18H4V4h2m3 11 2 2 5-5",
  settings: "M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Zm0-5v2m0 14v2m9-9h-2M5 12H3m15.36-6.36-1.42 1.42M7.06 16.94l-1.42 1.42m12.72 0-1.42-1.42M7.06 7.06 5.64 5.64",
  sparkles: "m12 3 1.9 5.8L20 11l-6.1 2.2L12 19l-1.9-5.8L4 11l6.1-2.2L12 3Zm7 11 1.2 2.8L23 18l-2.8 1.2L19 22l-1.2-2.8L15 18l2.8-1.2L19 14ZM5 2l.8 2.2L8 5l-2.2.8L5 8l-.8-2.2L2 5l2.2-.8L5 2Z",
  "shield-check": "M12 22s8-4 8-11V5l-8-3-8 3v6c0 7 8 11 8 11Zm-4-11 3 3 5-5",
  history: "M3 12a9 9 0 1 0 2.64-6.36L3 8m0-5v5h5m4-1v5l4 2",
  "chart-no-axes-combined": "M3 3v18h18M7 14l4-4 4 4 6-7m-5 0h5v5",
  "settings-2": "M20 7h-9m3 10H4m6-12a2 2 0 1 0 0 4 2 2 0 0 0 0-4Zm6 10a2 2 0 1 0 0 4 2 2 0 0 0 0-4ZM4 7h2m10 0h4m-16 10h10m6 0h0",
  search: "m21 21-4.3-4.3M19 10.5a8.5 8.5 0 1 1-17 0 8.5 8.5 0 0 1 17 0Z",
  "arrow-up-right": "M7 17 17 7M7 7h10v10",
  "log-out": "M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5m5 5H9",
  moon: "M20.9 13A9 9 0 0 1 11 3.1 9 9 0 1 0 20.9 13Z",
  sun: "M12 3v2m0 14v2m9-9h-2M5 12H3m15.36-6.36-1.42 1.42M7.06 16.94l-1.42 1.42m12.72 0-1.42-1.42M7.06 7.06 5.64 5.64M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z",
  monitor: "M3 4h18v13H3zM8 21h8m-4-4v4",
  eye: "M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7Zm10 3a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z"
};

/** Renders a product-neutral icon, hidden from assistive technology unless titled. */
export function Icon({ name, title, size = 20, width, height, ...props }: IconProps) {
  return <svg aria-hidden={title ? undefined : true} fill="none" focusable="false" role={title ? "img" : undefined} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" viewBox="0 0 24 24" width={width ?? size} height={height ?? size} {...props}>{title ? <title>{title}</title> : null}<path d={paths[name]} /></svg>;
}
