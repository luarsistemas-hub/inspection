"use client";

import { useState, type ReactNode } from "react";
import { Dialog } from "./dialog.js";
import { Icon } from "./icon.js";

export type MobileNavigationProps = { label: string; children: ReactNode; buttonLabel?: string };

/** Opens the product-owned mobile navigation in the shared accessible dialog. */
export function MobileNavigation({ label, children, buttonLabel = "Abrir navegação" }: MobileNavigationProps) {
  const [isOpen, setIsOpen] = useState(false);
  return <div className="inspection-mobile-navigation">
    <button aria-expanded={isOpen} aria-haspopup="dialog" aria-label={buttonLabel} className="inspection-mobile-navigation__trigger" onClick={() => setIsOpen(true)} type="button"><Icon name="menu" size={22} /></button>
    <Dialog isOpen={isOpen} onClose={() => setIsOpen(false)} title={label}>
      <div className="inspection-mobile-navigation__links" onClick={(event) => { if ((event.target as HTMLElement).closest("a, button")) setIsOpen(false); }}>{children}</div>
    </Dialog>
  </div>;
}
