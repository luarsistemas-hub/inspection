"use client";

import { FormEvent, useState } from "react";
import { beginPKCE } from "@/auth/pkce";
import { requestActivationCode, setInitialPassword, verifyActivationCode } from "@/auth/activation";

type Stage = "request" | "verify" | "password" | "ready";

export default function ActivatePage() {
  const [stage, setStage] = useState<Stage>("request");
  const [code, setCode] = useState(""); const [password, setPassword] = useState(""); const [confirmation, setConfirmation] = useState("");
  const [message, setMessage] = useState(""); const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setMessage("");
    try {
      const errors = stage === "request" ? await requestActivationCode() : stage === "verify" ? await verifyActivationCode(code) : await setInitialPassword(password);
      if (errors.length) { setMessage(errors[0].message); return; }
      setStage(stage === "request" ? "verify" : stage === "verify" ? "password" : "ready");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Não foi possível concluir a ativação."); } finally { setBusy(false); }
  };
  if (stage === "ready") return <main className="admin-denial"><h1>Senha criada</h1><p role="status">Use sua nova senha para entrar na administração.</p><button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", "/organization")}>Entrar na administração</button></main>;
  return <main className="admin-denial"><h1>Ative sua conta administrativa</h1><p>Confirme um código exclusivo de ativação antes de criar sua senha.</p><form className="admin-form" onSubmit={submit} noValidate>
    {stage === "request" ? <p>Enviaremos um código ao e-mail verificado no onboarding.</p> : null}
    {stage === "verify" ? <label>Código de ativação<input aria-label="Código de ativação" autoComplete="one-time-code" inputMode="numeric" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))} required /></label> : null}
    {stage === "password" ? <><label>Nova senha<input aria-label="Nova senha" autoComplete="new-password" type="password" minLength={12} value={password} onChange={(event) => setPassword(event.target.value)} required /></label><label>Confirme a nova senha<input aria-label="Confirme a nova senha" autoComplete="new-password" type="password" minLength={12} value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label>{password && confirmation && password !== confirmation ? <p role="alert">As senhas não coincidem.</p> : <p>A senha precisa ter pelo menos 12 caracteres.</p>}</> : null}
    {message ? <p role="alert">{message}</p> : null}
    <button type="submit" disabled={busy || (stage === "verify" && code.length !== 6) || (stage === "password" && (password.length < 12 || password !== confirmation))}>{busy ? "Processando…" : stage === "request" ? "Enviar código de ativação" : stage === "verify" ? "Confirmar código" : "Criar senha"}</button>
  </form></main>;
}
