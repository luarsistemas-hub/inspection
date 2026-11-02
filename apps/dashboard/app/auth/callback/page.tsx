"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { beginPKCE, takePKCE } from "@/auth/pkce";
import { setSession } from "@/auth/session";

export default function CallbackPage() {
  const router = useRouter(); const started = useRef(false); const [message, setMessage] = useState("Concluindo autenticação…"); const [failed, setFailed] = useState(false); const [returnTo, setReturnTo] = useState("/tenants/current");
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const query = new URLSearchParams(location.search); const error = query.get("error"); const code = query.get("code"); const attempt = takePKCE(query.get("state"));
    setReturnTo(attempt?.returnTo ?? "/tenants/current");
    if (error) { setMessage("A autenticação foi recusada. Entre novamente."); setFailed(true); return; }
    if (!code || !attempt) { setMessage("Não foi possível validar a autenticação. Entre novamente."); setFailed(true); return; }
    const endpoint = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL;
    if (!endpoint) { setMessage("A autenticação não está configurada neste ambiente."); setFailed(true); return; }
    void fetch(endpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, code_verifier: attempt.verifier, redirect_uri: `${location.origin}/auth/callback`, client_id: "inspection-dashboard" }) })
      .then(async (response) => {
        if (response.ok) return response.json() as Promise<{ access_token: string; id_token?: string; refresh_token?: string; expires_in?: number }>;
        console.error("OIDC token exchange failed", response.status, await response.text());
        throw new Error("OIDC token exchange failed");
      })
      .then(({ access_token, id_token, refresh_token, expires_in }) => { setSession(access_token, undefined, id_token, refresh_token, expires_in); router.replace(attempt.returnTo); })
      .catch((error: unknown) => { console.error("Dashboard authentication failed", error); setMessage("Não foi possível concluir a autenticação. Tente novamente."); setFailed(true); });
  }, [router]);
  return <main className="denial"><h1>Autenticação do Painel</h1><p role="status">{message}</p>{failed && <button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", returnTo)}>Entrar novamente</button>}</main>;
}
