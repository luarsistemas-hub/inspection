import type { ReactNode } from "react";
import { Icon, type IconName } from "./icon.js";

type Tone = "info" | "success" | "warning" | "danger";
const toneIcon: Record<Tone, IconName> = { info: "info", success: "check", warning: "warning", danger: "error" };
export type AlertProps = { children: ReactNode; tone?: Tone; title?: string };
export type StatusProps = { children: ReactNode; tone?: Tone };

/** Announces important feedback with text and an icon, never color alone. */
export function Alert({ children, tone = "info", title }: AlertProps) {
  return <div className={`inspection-alert inspection-alert--${tone}`} role={tone === "danger" ? "alert" : "status"}><Icon name={toneIcon[tone]} /><span>{title ? <strong>{title}: </strong> : null}{children}</span></div>;
}

/** Marks a compact state with both an icon and visible text. */
export function Status({ children, tone = "info" }: StatusProps) {
  return <span className={`inspection-status inspection-alert--${tone}`}><Icon name={toneIcon[tone]} aria-hidden="true" />{children}</span>;
}
