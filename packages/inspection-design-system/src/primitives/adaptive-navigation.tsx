"use client";

import { useState, type ReactNode } from "react";
import { Dialog } from "./dialog.js";
import { Icon, type IconName } from "./icon.js";

export type AdaptiveNavigationItem = {
  href: string;
  label: string;
  icon: IconName;
  badge?: string | number;
  group?: string;
};

export type AdaptiveNavigationProps = {
  label: string;
  activeHref: string;
  primaryItems: AdaptiveNavigationItem[];
  secondaryItems?: AdaptiveNavigationItem[];
  moreBadge?: string | number;
  renderLink: (item: AdaptiveNavigationItem, className: string, onNavigate?: () => void) => ReactNode;
};

/** Uses a bottom bar on compact windows, a rail on medium windows, and a full sidebar on large windows. */
export function AdaptiveNavigation({ label, activeHref, primaryItems, secondaryItems = [], moreBadge, renderLink }: AdaptiveNavigationProps) {
  const [moreOpen, setMoreOpen] = useState(false);
  const hasSecondaryActive = secondaryItems.some((item) => item.href === activeHref);
  const allItems = [...primaryItems, ...secondaryItems];
  const groupedItems = (items: AdaptiveNavigationItem[]) => {
    const groups = new Map<string, AdaptiveNavigationItem[]>();
    for (const item of items) {
      const group = item.group ?? "Destinos";
      groups.set(group, [...(groups.get(group) ?? []), item]);
    }
    return [...groups].map(([group, groupItems]) => ({ group, items: groupItems }));
  };

  const renderItem = (item: AdaptiveNavigationItem, placement: "sidebar" | "compact" | "dialog") => renderLink(
    item,
    `inspection-adaptive-navigation__item inspection-adaptive-navigation__item--${placement}`,
    placement === "dialog" ? () => setMoreOpen(false) : undefined,
  );

  const moreButton = (placement: "compact" | "rail") => <button
    aria-current={hasSecondaryActive ? "page" : undefined}
    aria-haspopup="dialog"
    aria-expanded={moreOpen}
    aria-label={moreBadge ? `Mais destinos, ${moreBadge} notificações não lidas` : "Mais destinos"}
    className={`inspection-adaptive-navigation__more inspection-adaptive-navigation__more--${placement}${hasSecondaryActive ? " is-active" : ""}`}
    onClick={() => setMoreOpen(true)}
    type="button"
  >
    <span className="inspection-adaptive-navigation__icon"><Icon name="more-horizontal" size={22} />{moreBadge ? <span className="inspection-adaptive-navigation__badge" aria-hidden="true">{moreBadge}</span> : null}</span>
    <span>Mais</span>
  </button>;

  return <>
    <nav aria-label={label} className="inspection-adaptive-navigation__sidebar">
      <span className="inspection-adaptive-navigation__section">{label}</span>
      <div className="inspection-adaptive-navigation__group">{primaryItems.map((item) => <span key={item.href}>{renderItem(item, "sidebar")}</span>)}</div>
      {groupedItems(secondaryItems).map(({ group, items }) => <div className="inspection-adaptive-navigation__group inspection-adaptive-navigation__group--secondary" key={group}><span className="inspection-adaptive-navigation__section">{group}</span>{items.map((item) => <span key={item.href}>{renderItem(item, "sidebar")}</span>)}</div>)}
      {secondaryItems.length ? moreButton("rail") : null}
    </nav>
    <nav aria-label={label} className="inspection-adaptive-navigation__compact" data-item-count={primaryItems.length + (secondaryItems.length ? 1 : 0)}>
      {primaryItems.map((item) => <span key={item.href}>{renderItem(item, "compact")}</span>)}
      {secondaryItems.length ? moreButton("compact") : null}
    </nav>
    {secondaryItems.length ? <Dialog isOpen={moreOpen} onClose={() => setMoreOpen(false)} title={label}>
      <nav aria-label={`${label} — outros destinos`} className="inspection-adaptive-navigation__dialog">
        {groupedItems(allItems).map(({ group, items }) => <section className="inspection-adaptive-navigation__dialog-group" key={group}><h3>{group}</h3>{items.map((item) => renderItem(item, "dialog"))}</section>)}
      </nav>
    </Dialog> : null}
  </>;
}
