import type { ButtonHTMLAttributes, ReactNode } from "react";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  children: ReactNode;
  variant?: "primary" | "secondary";
};

/** Renders an accessible action button with a consistent touch target and focus treatment. */
export function Button({ children, className = "", variant = "primary", type = "button", ...props }: ButtonProps) {
  return <button className={`inspection-button inspection-button--${variant} ${className}`.trim()} type={type} {...props}>{children}</button>;
}
