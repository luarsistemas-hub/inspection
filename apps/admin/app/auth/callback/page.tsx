"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { beginPKCE, takePKCE } from "@/auth/pkce";
import { setSession } from "@/auth/session";
import { Button, Recovery } from "@inspection/design-system";

export default function CallbackPage() {
  const router = useRouter(); const started = useRef(false); const [message, setMessage] = useState("Concluindo autenticação…"); const [failed, setFailed] = useState(false); const [returnTo, setReturnTo] = useState("/organization");
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const query = new URLSearchParams(location.search); const error = query.get("error"); const code = query.get("code"); const attempt = takePKCE(query.get("state"));
    setReturnTo(attempt?.returnTo ?? "/organization");
    if (error) { setMessage("A autenticação foi recusada. Entre novamente."); setFailed(true); return; }
    if (!code || !attempt) { setMessage("Não foi possível validar a autenticação. Entre novamente."); setFailed(true); return; }
    const tokenEndpoint = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL;
    if (!tokenEndpoint) { setMessage("A autenticação não está configurada neste ambiente."); setFailed(true); return; }
    void fetch(tokenEndpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, code_verifier: attempt.verifier, redirect_uri: `${location.origin}/auth/callback`, client_id: "inspection-admin" }) })
      .then(async (response) => {
        if (response.ok) return response.json() as Promise<{ access_token: string; id_token?: string }>;
        console.error("OIDC token exchange failed", response.status, await response.text());
        throw new Error("OIDC token exchange failed");
      })
      .then(({ access_token, id_token }) => { setSession(access_token, undefined, id_token); router.replace(attempt.returnTo); })
      .catch((error: unknown) => { console.error("Admin authentication failed", error); setMessage("Não foi possível concluir a autenticação. Tente novamente."); setFailed(true); });
  }, [router]);
  return <main className="admin-denial"><h1>Autenticação administrativa</h1><Recovery kind={failed ? "error" : "loading"} title={failed ? "Não foi possível entrar" : "Concluindo autenticação"}>{message}</Recovery>{failed && <Button onClick={() => void beginPKCE(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/auth", returnTo)}>Entrar novamente</Button>}</main>;
}
