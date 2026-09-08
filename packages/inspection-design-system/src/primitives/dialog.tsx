import { useEffect, useRef, type ReactNode } from "react";

export type DialogProps = { children: ReactNode; isOpen: boolean; onClose: () => void; title: string };

/** Provides a modal dialog with Escape handling and initial focus for keyboard users. */
export function Dialog({ children, isOpen, onClose, title }: DialogProps) {
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!isOpen) return;
    dialogRef.current?.focus();
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
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
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;
  return <div className="inspection-backdrop" onMouseDown={onClose}>
    <div aria-labelledby="inspection-dialog-title" aria-modal="true" className="inspection-dialog" onMouseDown={(event) => event.stopPropagation()} ref={dialogRef} role="dialog" tabIndex={-1}>
      <h2 id="inspection-dialog-title">{title}</h2>
      {children}
    </div>
  </div>;
}
