import type { SVGProps } from "react";

export type IconName = "check" | "info" | "warning" | "error" | "menu" | "arrow-right" | "camera";
export type IconProps = SVGProps<SVGSVGElement> & { name: IconName; title?: string };

const paths: Record<IconName, string> = {
  check: "M5 12l4 4L19 6",
  info: "M12 8h.01M11 12h1v4h1M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z",
  warning: "M12 3 2 21h20L12 3Zm0 6v5m0 3h.01",
  error: "M12 9v4m0 4h.01M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z",
  menu: "M4 7h16M4 12h16M4 17h16",
  "arrow-right": "M5 12h14m-5-5 5 5-5 5",
  camera: "M4 7h4l1.5-2h5L16 7h4v12H4V7Zm8 3a3 3 0 1 0 0 6 3 3 0 0 0 0-6Z"
};

/** Renders a product-neutral icon, hidden from assistive technology unless titled. */
export function Icon({ name, title, ...props }: IconProps) {
  return <svg aria-hidden={title ? undefined : true} fill="none" focusable="false" role={title ? "img" : undefined} stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" viewBox="0 0 24 24" {...props}>{title ? <title>{title}</title> : null}<path d={paths[name]} /></svg>;
}
