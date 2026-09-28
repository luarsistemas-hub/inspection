"use client";

import React, { useEffect, useState } from "react";
import { Confirmation, Dialog, Field, Textarea } from "@inspection/design-system";

type ConfirmationDialogProps = {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: (reason?: string) => Promise<void> | void;
  title: string;
  target: string;
  scope: string;
  consequence: string;
  confirmLabel: string;
  reasonLabel?: string;
};

/** Composes shared modal mechanics with the feature-owned action and reason. */
export function ConfirmationDialog({
  isOpen,
  onClose,
  onConfirm,
  title,
  target,
  scope,
  consequence,
  confirmLabel,
  reasonLabel,
}: ConfirmationDialogProps) {
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (isOpen) {
      setReason("");
      setError("");
      setBusy(false);
    }
  }, [isOpen]);

  const confirm = async () => {
    const value = reason.trim();
    if (reasonLabel && !value) {
      setError("Informe o motivo para continuar.");
      return;
    }
    setBusy(true);
    try {
      await onConfirm(value || undefined);
      onClose();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Não foi possível concluir a operação.");
    } finally {
      setBusy(false);
    }
  };

  return <Dialog isOpen={isOpen} isDismissable={!busy} onClose={() => { if (!busy) onClose(); }} title={title}>
    <div className="confirmation-dialog">
      {reasonLabel && <Field label={reasonLabel} error={error} required><Textarea value={reason} rows={3} onChange={(event) => setReason(event.target.value)} /></Field>}
      {!reasonLabel && error && <p className="warning" role="alert">{error}</p>}
      <Confirmation
        consequence={consequence}
        confirmLabel={confirmLabel}
        isConfirmDisabled={Boolean(reasonLabel && !reason.trim())}
        isPending={busy}
        onCancel={onClose}
        onConfirm={() => void confirm()}
        reason={reason.trim() || undefined}
        scope={scope}
        target={target}
      />
    </div>
  </Dialog>;
}
