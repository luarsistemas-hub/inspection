import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  children: ReactNode;
  variant?: "primary" | "secondary" | "tonal" | "text" | "danger";
  /** Prevents repeated activation while the owning feature has work in flight. */
  isPending?: boolean;
  pendingLabel?: ReactNode;
};

/** Renders an accessible action button with a consistent touch target and focus treatment. */
export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button({ children, className = "", variant = "primary", type = "button", isPending = false, pendingLabel, disabled, ...props }, ref) {
  return <button aria-busy={isPending || undefined} className={`inspection-button inspection-button--${variant} ${className}`.trim()} disabled={disabled || isPending} ref={ref} type={type} {...props}>{isPending && pendingLabel ? pendingLabel : children}</button>;
});
