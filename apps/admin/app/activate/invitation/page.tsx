"use client";

import { FormEvent, useRef, useState } from "react";
import { Alert, Button, Field, Input, Recovery, Steps } from "@inspection/design-system";
import { beginPKCE } from "@/auth/pkce";
import { completeInvitation, requestInvitationCode, verifyInvitationCode } from "@/auth/user-invitation-activation";

type Stage = "request" | "verify" | "password" | "ready";

export default function InvitationActivationPage() {
  const [stage, setStage] = useState<Stage>("request");
  const [activation, setActivation] = useState<{ email: string; newIdentity: boolean }>();
  const [code, setCode] = useState(""); const [password, setPassword] = useState(""); const [confirmation, setConfirmation] = useState("");
  const [message, setMessage] = useState(""); const [busy, setBusy] = useState(false); const submitting = useRef(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (submitting.current) return;
    submitting.current = true; setBusy(true); setMessage("");
    try {
      const token = stage === "request" && typeof window !== "undefined" ? new URLSearchParams(window.location.search).get("token") : null;
      const result = stage === "request" ? await requestInvitationCode(token) : stage === "verify" ? await verifyInvitationCode(code) : await completeInvitation(activation?.newIdentity ? password : null);
      if (result.userErrors.length) { setMessage(result.userErrors[0].message); return; }
      if (stage === "request") { if (!result.activation) throw new Error("O convite não retornou os dados de ativação."); setActivation({ email: result.activation.email, newIdentity: result.activation.newIdentity }); setStage("verify"); }
      else if (stage === "verify") setStage(activation?.newIdentity ? "password" : "ready");
      else setStage("ready");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Não foi possível concluir a ativação."); }
    finally { submitting.current = false; setBusy(false); }
  };
  if (stage === "ready") return <main className="admin-denial"><Recovery kind="empty" title="Acesso ativado">Agora entre com sua conta. Se você já tinha uma senha, ela continua a mesma.</Recovery><Button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", "/organization")}>Entrar na administração</Button></main>;
  const passwordValid = password.length >= 6 && /\p{Lu}/u.test(password) && /[\p{P}\p{S}]/u.test(password) && password === confirmation;
  return <main className="admin-denial"><h1>Aceitar convite de acesso</h1><p>Confirme o seu e-mail para ativar o acesso ao Inspection.</p><form className="admin-form" onSubmit={submit} noValidate>
    <Steps label="Progresso da ativação" items={[{ id: "request", label: "Solicitar código", state: stage === "request" ? "current" : "complete" }, { id: "verify", label: "Confirmar código", state: stage === "verify" ? "current" : stage === "password" ? "complete" : "pending" }, ...(activation?.newIdentity ? [{ id: "password", label: "Criar senha", state: stage === "password" ? "current" as const : "pending" as const }] : [])]} />
    {stage === "request" && <p>Enviaremos um código de confirmação para o e-mail do convite.</p>}
    {stage === "verify" && <><p>Código enviado para <strong>{activation?.email}</strong>.</p><Field label="Código de confirmação" required error={code && code.length !== 6 ? "Informe os seis dígitos do código." : undefined}><Input aria-label="Código de confirmação" autoComplete="one-time-code" inputMode="numeric" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))} /></Field></>}
    {stage === "password" && <><Field label="Nova senha" required hint="Use pelo menos 6 caracteres, uma letra maiúscula e um caractere especial."><Input autoComplete="new-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} /></Field><Field label="Confirme a nova senha" required error={confirmation && password !== confirmation ? "As senhas não coincidem." : undefined}><Input autoComplete="new-password" type="password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></Field></>}
    {message && <Alert tone="danger">{message}</Alert>}
    <Button type="submit" isPending={busy} pendingLabel="Processando…" disabled={(stage === "verify" && code.length !== 6) || (stage === "password" && !passwordValid)}>{stage === "request" ? "Enviar código" : stage === "verify" ? "Confirmar código" : "Criar senha e ativar"}</Button>
  </form></main>;
}
