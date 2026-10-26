"use client";

import { useEffect, useRef, type ReactNode, type RefObject } from "react";
import { Dialog as AriaDialog, Modal, ModalOverlay } from "react-aria-components";
import { Icon } from "./icon.js";

export type DialogProps = { children: ReactNode; isOpen: boolean; onClose: () => void; title: string; size?: "default" | "wide" | "fullscreen"; isDismissable?: boolean; restoreFocusRef?: RefObject<HTMLElement | null> };

/** Provides a modal dialog with Escape handling and initial focus for keyboard users. */
export function Dialog({ children, isOpen, onClose, title, size = "default", isDismissable = true, restoreFocusRef }: DialogProps) {
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const wasOpenRef = useRef(false);
  useEffect(() => {
    const trackFocus = (event: FocusEvent) => {
      const target = event.target;
      if (!(target instanceof HTMLElement) || target === document.body || target === document.documentElement) return;
      if (target.closest(".inspection-backdrop, dialog[open]")) return;
      previousFocusRef.current = target;
    };
    const trackPointer = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Element) || target.closest(".inspection-backdrop, dialog[open]")) return;
      // A press on non-focusable content leaves nothing to restore; the close
      // path then falls back to the page heading.
      previousFocusRef.current = target.closest<HTMLElement>('button, a, input, select, textarea, [tabindex]:not([tabindex="-1"])');
    };
    document.addEventListener("focusin", trackFocus);
    document.addEventListener("pointerdown", trackPointer);
    return () => {
      document.removeEventListener("focusin", trackFocus);
      document.removeEventListener("pointerdown", trackPointer);
    };
  }, []);
  useEffect(() => {
    if (isOpen) {
      wasOpenRef.current = true;
      return;
    }
    if (!wasOpenRef.current) return;
    wasOpenRef.current = false;
    const target = restoreFocusRef?.current ?? previousFocusRef.current;
    const restoreFocus = () => {
      if (document.querySelector('[aria-modal="true"]')) return;
      if (target && document.contains(target)) {
        target.focus({ preventScroll: true });
        return;
      }
      const fallback = document.querySelector<HTMLElement>("main h1, h1");
      if (fallback) {
        fallback.tabIndex = -1;
        fallback.focus({ preventScroll: true });
      }
    };
    // ModalOverlay and FocusScope restore their own focus during the close
    // commit. Restore after that work so nested dialogs cannot steal focus
    // back to an element that is being removed with the parent dialog.
    // Keep this scheduled work alive when the dialog itself is conditionally
    // unmounted as part of closing (for example, the compact inspection detail).
    window.setTimeout(() => {
      window.requestAnimationFrame(() => window.requestAnimationFrame(restoreFocus));
    }, 0);
  }, [isOpen, restoreFocusRef]);
  return <ModalOverlay className="inspection-backdrop" isDismissable={isDismissable} isKeyboardDismissDisabled={!isDismissable} isOpen={isOpen} onOpenChange={(open) => { if (!open) onClose(); }}>
    <Modal className={`inspection-dialog inspection-dialog--${size}`}>
      <AriaDialog aria-label={title} className="inspection-dialog__content"><header className="inspection-dialog__header"><h2>{title}</h2>{isDismissable ? <button aria-label="Fechar" className="inspection-dialog__close" onClick={onClose} type="button"><Icon name="close" size={20} /></button> : null}</header>{children}</AriaDialog>
    </Modal>
  </ModalOverlay>;
}
