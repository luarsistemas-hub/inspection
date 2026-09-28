"use client";

import { FormEvent, useRef, useState } from "react";
import { Alert, Button, Field, Input, Recovery, Steps } from "@inspection/design-system";
import { beginPKCE } from "@/auth/pkce";
import { isInitialPasswordValid, requestActivationCode, setInitialPassword, verifyActivationCode } from "@/auth/activation";

type Stage = "request" | "verify" | "password" | "ready";

export default function ActivatePage() {
  const [stage, setStage] = useState<Stage>("request");
  const [code, setCode] = useState(""); const [password, setPassword] = useState(""); const [confirmation, setConfirmation] = useState("");
  const [message, setMessage] = useState(""); const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting.current) return;
    submitting.current = true; setBusy(true); setMessage("");
    try {
      const activationToken = typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("token");
      const errors = stage === "request" ? await requestActivationCode(activationToken) : stage === "verify" ? await verifyActivationCode(code) : await setInitialPassword(password);
      if (errors.length) { setMessage(errors[0].message); return; }
      setStage(stage === "request" ? "verify" : stage === "verify" ? "password" : "ready");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Não foi possível concluir a ativação."); } finally { submitting.current = false; setBusy(false); }
  };
  if (stage === "ready") return <main className="admin-denial"><Recovery kind="empty" title="Senha criada">Use sua nova senha para entrar na administração.</Recovery><Button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", "/organization")}>Entrar na administração</Button></main>;
  return <main className="admin-denial"><h1>Ative sua conta administrativa</h1><p>Confirme um código exclusivo de ativação antes de criar sua senha.</p><form className="admin-form" onSubmit={submit} noValidate>
    <Steps label="Progresso da ativação" items={[{ id: "request", label: "Solicitar código", state: stage === "request" ? "current" : "complete" }, { id: "verify", label: "Confirmar código", state: stage === "verify" ? "current" : stage === "password" ? "complete" : "pending" }, { id: "password", label: "Criar senha", state: stage === "password" ? "current" : "pending" }]} />
    {stage === "request" ? <p>Enviaremos um código ao e-mail verificado no onboarding.</p> : null}
    {stage === "verify" ? <Field label="Código de ativação" required error={code && code.length !== 6 ? "Informe os seis dígitos do código." : undefined}><Input aria-label="Código de ativação" autoComplete="one-time-code" inputMode="numeric" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))} /></Field> : null}
    {stage === "password" ? <><Field label="Nova senha" required hint="A senha precisa ter pelo menos 12 caracteres." error={password && password.length < 12 ? "Use ao menos 12 caracteres." : undefined}><Input aria-label="Nova senha" autoComplete="new-password" type="password" minLength={12} value={password} onChange={(event) => setPassword(event.target.value)} /></Field><Field label="Confirme a nova senha" required error={confirmation && password !== confirmation ? "As senhas não coincidem." : undefined}><Input aria-label="Confirme a nova senha" autoComplete="new-password" type="password" minLength={12} value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></Field></> : null}
    {message ? <Alert tone="danger">{message}</Alert> : null}
    <Button type="submit" isPending={busy} pendingLabel="Processando…" disabled={(stage === "verify" && code.length !== 6) || (stage === "password" && !isInitialPasswordValid(password, confirmation))}>{stage === "request" ? "Enviar código de ativação" : stage === "verify" ? "Confirmar código" : "Criar senha"}</Button>
  </form></main>;
}
