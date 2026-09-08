"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { takePKCE } from "@/auth/pkce";
import { setSession } from "@/auth/session";

export default function CallbackPage() {
  const router = useRouter(); const started = useRef(false); const [message, setMessage] = useState("Concluindo autenticação…");
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const query = new URLSearchParams(location.search); const error = query.get("error"); const code = query.get("code"); const attempt = takePKCE(query.get("state"));
    if (error) { setMessage("A autenticação foi recusada. Entre novamente."); return; }
    if (!code || !attempt) { setMessage("Não foi possível validar a autenticação. Entre novamente."); return; }
    const tokenEndpoint = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL;
    if (!tokenEndpoint) { setMessage("A autenticação não está configurada neste ambiente."); return; }
    void fetch(tokenEndpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, code_verifier: attempt.verifier, redirect_uri: `${location.origin}/auth/callback`, client_id: "inspection-admin" }) })
      .then(async (response) => {
        if (response.ok) return response.json() as Promise<{ access_token: string }>;
        console.error("OIDC token exchange failed", response.status, await response.text());
        throw new Error("OIDC token exchange failed");
      })
      .then(({ access_token }) => { setSession(access_token); router.replace(attempt.returnTo); })
      .catch((error: unknown) => { console.error("Admin authentication failed", error); setMessage("Não foi possível concluir a autenticação. Tente novamente."); });
  }, [router]);
  return <main><h1>Autenticação administrativa</h1><p role="status">{message}</p></main>;
}
