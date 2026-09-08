import type { ReactNode } from "react";

export type NavigationItem = { href: string; label: ReactNode; current?: boolean };
export type NavigationProps = { items: NavigationItem[]; label: string };

/** Renders an accessible, product-composed navigation list. */
export function Navigation({ items, label }: NavigationProps) {
  return <nav aria-label={label} className="inspection-navigation">{items.map((item) => <a aria-current={item.current ? "page" : undefined} href={item.href} key={item.href}>{item.label}</a>)}</nav>;
}
