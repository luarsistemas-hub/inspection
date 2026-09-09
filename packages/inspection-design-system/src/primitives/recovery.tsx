import type { ReactNode } from "react";

export type RecoveryProps = { title: string; children: ReactNode; onRetry?: () => void; retryLabel?: string };

export function Recovery({ title, children, onRetry, retryLabel = "Tentar novamente" }: RecoveryProps) {
  return <section className="inspection-recovery" aria-labelledby="inspection-recovery-title"><h2 id="inspection-recovery-title">{title}</h2><div role="status" aria-live="polite">{children}</div>{onRetry ? <button className="inspection-button inspection-button--secondary" type="button" onClick={onRetry}>{retryLabel}</button> : null}</section>;
}

export type VersionConflictProps = { currentVersion: number; children: ReactNode; onReview: () => void };

export function VersionConflict({ currentVersion, children, onReview }: VersionConflictProps) {
  return <section className="inspection-version-conflict" role="alert"><h2>Alteração simultânea detectada</h2><p>A versão atual é {currentVersion}. Revise os valores antes de salvar.</p>{children}<button className="inspection-button inspection-button--secondary" type="button" onClick={onReview}>Revisar valores atuais</button></section>;
}

export type ConfirmationProps = { target: string; scope: string; consequence: string; reason?: string; onCancel: () => void; onConfirm: () => void; confirmLabel?: string };

export function Confirmation({ target, scope, consequence, reason, onCancel, onConfirm, confirmLabel = "Confirmar" }: ConfirmationProps) {
  return <section className="inspection-confirmation" aria-label="Confirmação de ação"><h2>Confirme a ação</h2><dl><dt>Alvo</dt><dd>{target}</dd><dt>Escopo</dt><dd>{scope}</dd><dt>Consequência</dt><dd>{consequence}</dd>{reason ? <><dt>Motivo</dt><dd>{reason}</dd></> : null}</dl><div className="inspection-inline"><button className="inspection-button inspection-button--secondary" type="button" onClick={onCancel}>Cancelar</button><button className="inspection-button inspection-button--primary" type="button" onClick={onConfirm}>{confirmLabel}</button></div></section>;
}
