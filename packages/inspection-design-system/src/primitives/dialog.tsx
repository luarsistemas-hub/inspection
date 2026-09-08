import { useEffect, useId, useRef, type ReactNode } from "react";

export type DialogProps = { children: ReactNode; isOpen: boolean; onClose: () => void; title: string };

/** Provides a modal dialog with Escape handling and initial focus for keyboard users. */
export function Dialog({ children, isOpen, onClose, title }: DialogProps) {
  const titleId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const openerRef = useRef<HTMLElement | null>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!isOpen) return;
    openerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialogRef.current?.focus();
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onCloseRef.current();
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>("a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])"));
      if (focusable.length === 0) {
        event.preventDefault();
        dialogRef.current.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable.at(-1)!;
      const focusIsInsideDialog = dialogRef.current.contains(document.activeElement);
      const focusIsOnDialog = document.activeElement === dialogRef.current;
      if (event.shiftKey && (!focusIsInsideDialog || focusIsOnDialog || document.activeElement === first)) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (!focusIsInsideDialog || focusIsOnDialog || document.activeElement === last)) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", keydown);
    return () => {
      window.removeEventListener("keydown", keydown);
      if (openerRef.current && document.contains(openerRef.current)) {
        openerRef.current.focus();
      }
      openerRef.current = null;
    };
  }, [isOpen]);

  if (!isOpen) return null;
  return <div className="inspection-backdrop" onMouseDown={onClose}>
    <div aria-labelledby={titleId} aria-modal="true" className="inspection-dialog" onMouseDown={(event) => event.stopPropagation()} ref={dialogRef} role="dialog" tabIndex={-1}>
      <h2 id={titleId}>{title}</h2>
      {children}
    </div>
  </div>;
}
