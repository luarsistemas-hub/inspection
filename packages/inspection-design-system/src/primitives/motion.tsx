import type { HTMLAttributes, ReactNode } from "react";

export type MotionProps = HTMLAttributes<HTMLDivElement> & { children: ReactNode };

/** Adds opt-in decorative motion that is disabled by the package reduced-motion contract. */
export function Motion({ children, className = "", ...props }: MotionProps) {
  return <div className={`inspection-motion ${className}`.trim()} {...props}>{children}</div>;
}
