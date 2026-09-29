"use client";

import { useEffect, useRef, type ReactNode, type RefObject } from "react";
import { Dialog as AriaDialog, Modal, ModalOverlay } from "react-aria-components";
import { Icon } from "./icon.js";

export type DialogProps = { children: ReactNode; isOpen: boolean; onClose: () => void; title: string; size?: "default" | "wide" | "fullscreen"; isDismissable?: boolean; restoreFocusRef?: RefObject<HTMLElement | null> };

/** Provides a modal dialog with Escape handling and initial focus for keyboard users. */
export function Dialog({ children, isOpen, onClose, title, size = "default", isDismissable = true, restoreFocusRef }: DialogProps) {
  const previousFocusRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (isOpen) {
      previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      return;
    }
    const target = restoreFocusRef?.current ?? previousFocusRef.current;
    if (target && document.contains(target)) target.focus();
    else {
      const fallback = document.querySelector<HTMLElement>("main h1, h1");
      if (fallback) {
        fallback.tabIndex = -1;
        window.setTimeout(() => { if (document.contains(fallback)) fallback.focus(); }, 0);
      }
    }
  }, [isOpen, restoreFocusRef]);
  return <ModalOverlay className="inspection-backdrop" isDismissable={isDismissable} isKeyboardDismissDisabled={!isDismissable} isOpen={isOpen} onOpenChange={(open) => { if (!open) onClose(); }}>
    <Modal className={`inspection-dialog inspection-dialog--${size}`}>
      <AriaDialog aria-label={title} className="inspection-dialog__content"><header className="inspection-dialog__header"><h2>{title}</h2>{isDismissable ? <button aria-label="Fechar" className="inspection-dialog__close" onClick={onClose} type="button"><Icon name="close" size={20} /></button> : null}</header>{children}</AriaDialog>
    </Modal>
  </ModalOverlay>;
}
