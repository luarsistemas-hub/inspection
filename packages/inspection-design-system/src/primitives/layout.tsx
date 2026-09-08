import type { CSSProperties, HTMLAttributes, ReactNode } from "react";

type Gap = "1" | "2" | "3" | "4" | "5" | "6";
export type ContainerProps = HTMLAttributes<HTMLDivElement> & { children: ReactNode };
export type StackProps = HTMLAttributes<HTMLDivElement> & { children: ReactNode; gap?: Gap };
export type InlineProps = HTMLAttributes<HTMLDivElement> & { children: ReactNode; gap?: Gap };

/** Centers responsive content without imposing product-level navigation or page layout. */
export function Container({ children, className = "", ...props }: ContainerProps) {
  return <div className={`inspection-container ${className}`.trim()} {...props}>{children}</div>;
}

/** Vertically groups related content with token-based spacing. */
export function Stack({ children, className = "", gap = "4", style, ...props }: StackProps) {
  return <div className={`inspection-stack ${className}`.trim()} style={{ "--inspection-stack-gap": `var(--inspection-space-${gap})`, ...style } as CSSProperties} {...props}>{children}</div>;
}

/** Wraps inline content so controls remain usable at narrow viewports. */
export function Inline({ children, className = "", gap = "2", style, ...props }: InlineProps) {
  return <div className={`inspection-inline ${className}`.trim()} style={{ "--inspection-inline-gap": `var(--inspection-space-${gap})`, ...style } as CSSProperties} {...props}>{children}</div>;
}

/** Provides neutral grouped content styling; product shells choose their own composition. */
export function Card({ children, className = "", ...props }: HTMLAttributes<HTMLElement> & { children: ReactNode }) {
  return <section className={`inspection-card ${className}`.trim()} {...props}>{children}</section>;
}
