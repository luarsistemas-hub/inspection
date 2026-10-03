"use client";

import React, { useEffect, useState, type ReactNode, type RefObject } from "react";
import { Confirmation, Dialog } from "@inspection/design-system";

/** Hosts a create or edit form without moving the page content below it. */
export function FormDialog({ isOpen, onClose, title, busy, error, children, restoreFocusRef }: {
  isOpen: boolean;
  onClose: () => void;
  title: string;
  busy: boolean;
  error?: string;
  children: ReactNode;
  restoreFocusRef?: RefObject<HTMLElement | null>;
}) {
  const [dirty, setDirty] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  useEffect(() => { if (!isOpen) { setDirty(false); setConfirmDiscard(false); } }, [isOpen]);

  const requestClose = () => {
    if (busy) return;
    if (dirty) { setConfirmDiscard(true); return; }
    onClose();
  };

  return <Dialog isOpen={isOpen} isDismissable={!busy && !confirmDiscard} onClose={requestClose} restoreFocusRef={restoreFocusRef} size="fullscreen" title={title}>
    <div className="form-dialog-body" onChangeCapture={() => setDirty(true)} onInputCapture={() => setDirty(true)} onClickCapture={(event) => {
      if (!(event.target instanceof Element)) return;
      if (event.target.closest('[role="option"]')) setDirty(true);
      if (event.target.closest('button[type="button"]') && !event.target.closest(".form-dialog-close, .form-dialog-actions")) setDirty(true);
    }} onKeyDownCapture={(event) => {
      if (event.key === "Enter" && event.target instanceof Element && event.target.getAttribute("role") === "combobox") setDirty(true);
    }}>
      <button type="button" className="form-dialog-close secondary" onClick={requestClose} disabled={busy}>Fechar</button>
      {busy && <p role="status">Salvando…</p>}
      {error && <p className="warning" role="alert">{error}</p>}
      {children}
      <div className="form-dialog-actions">
        <button type="button" className="secondary" onClick={requestClose} disabled={busy}>Cancelar</button>
      </div>
      <Dialog isOpen={confirmDiscard} isDismissable={!busy} onClose={() => setConfirmDiscard(false)} title="Descartar alterações?">
        <Confirmation target={title} scope="Formulário em edição" consequence="As alterações não salvas serão perdidas." onCancel={() => setConfirmDiscard(false)} onConfirm={() => { setConfirmDiscard(false); window.setTimeout(onClose, 0); }} confirmLabel="Descartar alterações" />
      </Dialog>
    </div>
  </Dialog>;
}
