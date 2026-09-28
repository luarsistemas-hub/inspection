import { useId, type ReactNode } from "react";

export type RecoveryKind = "loading" | "empty" | "error" | "unavailable" | "denied";
export type RecoveryProps = { title: string; children: ReactNode; kind?: RecoveryKind; onRetry?: () => void; retryLabel?: string };

export function Recovery({ title, children, kind = "error", onRetry, retryLabel = "Tentar novamente" }: RecoveryProps) {
  const headingId = useId();
  return <section className={`inspection-recovery inspection-recovery--${kind}`} aria-labelledby={headingId}><h2 id={headingId}>{title}</h2><div role={kind === "error" ? "alert" : "status"}>{children}</div>{onRetry ? <button className="inspection-button inspection-button--secondary" type="button" onClick={onRetry}>{retryLabel}</button> : null}</section>;
}

export type VersionConflictProps = { currentVersion: number; children: ReactNode; onReview: () => void };

export function VersionConflict({ currentVersion, children, onReview }: VersionConflictProps) {
  return <section className="inspection-version-conflict" role="alert"><h2>Alteração simultânea detectada</h2><p>A versão atual é {currentVersion}. Revise os valores antes de salvar.</p>{children}<button className="inspection-button inspection-button--secondary" type="button" onClick={onReview}>Revisar valores atuais</button></section>;
}

export type ConfirmationProps = { target: string; scope: string; consequence: string; reason?: string; onCancel: () => void; onConfirm: () => void; confirmLabel?: string; isPending?: boolean; isConfirmDisabled?: boolean };

export function Confirmation({ target, scope, consequence, reason, onCancel, onConfirm, confirmLabel = "Confirmar", isPending = false, isConfirmDisabled = false }: ConfirmationProps) {
  return <section className="inspection-confirmation" aria-label="Confirmação de ação"><h2>Confirme a ação</h2><dl><dt>Alvo</dt><dd>{target}</dd><dt>Escopo</dt><dd>{scope}</dd><dt>Consequência</dt><dd>{consequence}</dd>{reason ? <><dt>Motivo</dt><dd>{reason}</dd></> : null}</dl><div className="inspection-inline"><button className="inspection-button inspection-button--secondary" disabled={isPending} type="button" onClick={onCancel}>Cancelar</button><button aria-busy={isPending || undefined} className="inspection-button inspection-button--primary" disabled={isPending || isConfirmDisabled} type="button" onClick={onConfirm}>{confirmLabel}</button></div></section>;
}
