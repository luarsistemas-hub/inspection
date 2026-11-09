"use client";

export function ScopeConflictNotice({ message, onReload }: { message: string; onReload: () => void }) {
  return <p role="alert">{message} <button type="button" onClick={onReload}>Recarregar escopo</button></p>;
}
