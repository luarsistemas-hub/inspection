"use client";

import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Icon } from "./icon.js";

export type AccountMenuProps = {
  name: string;
  description?: string;
  email?: string;
  children?: ReactNode;
  onSignOut: () => void;
};

/** Shows the signed-in user's initial and a compact profile menu with sign-out. */
export function AccountMenu({ name, description, email, children, onSignOut }: AccountMenuProps) {
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuId = useId();
  const displayName = name.trim() || email?.trim() || "Conta";
  const initial = Array.from(displayName)[0]?.toLocaleUpperCase("pt-BR") ?? "C";

  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !containerRef.current?.contains(event.target)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const signOut = () => {
    setOpen(false);
    onSignOut();
  };

  return <div className="inspection-account-menu" ref={containerRef}>
    <button
      ref={triggerRef}
      className="inspection-account-menu__trigger"
      type="button"
      aria-label={`Abrir perfil de ${displayName}`}
      aria-controls={menuId}
      aria-expanded={open}
      onClick={() => setOpen((current) => !current)}
    >{initial}</button>
    {open ? <section className="inspection-account-menu__popover" id={menuId} aria-label={`Perfil de ${displayName}`}>
      <div className="inspection-account-menu__summary">
        <span className="inspection-account-menu__avatar" aria-hidden="true">{initial}</span>
        <div className="inspection-account-menu__identity">
          <strong>{displayName}</strong>
          {description ? <span>{description}</span> : null}
          {email ? <span>{email}</span> : null}
        </div>
      </div>
      {children ? <div className="inspection-account-menu__details">{children}</div> : null}
      <div className="inspection-account-menu__divider" />
      <button className="inspection-account-menu__sign-out" type="button" onClick={signOut}>Sair da conta<Icon name="log-out" size={18} /></button>
    </section> : null}
  </div>;
}
