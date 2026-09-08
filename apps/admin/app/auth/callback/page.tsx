"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { takePKCE } from "@/auth/pkce";
import { setSession } from "@/auth/session";

export default function CallbackPage() {
  const router = useRouter(); const [message, setMessage] = useState("Concluindo autenticação…");
  useEffect(() => {
    const query = new URLSearchParams(location.search); const code = query.get("code"); const attempt = takePKCE(query.get("state"));
    if (!code || !attempt) { setMessage("Não foi possível validar a autenticação. Entre novamente."); return; }
    const tokenEndpoint = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL;
    if (!tokenEndpoint) { setMessage("A autenticação não está configurada neste ambiente."); return; }
    void fetch(tokenEndpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, code_verifier: attempt.verifier, redirect_uri: `${location.origin}/auth/callback`, client_id: "inspection-admin" }) })
      .then((response) => response.ok ? response.json() as Promise<{ access_token: string }> : Promise.reject())
      .then(({ access_token }) => { setSession(access_token); router.replace(attempt.returnTo); })
      .catch(() => setMessage("Não foi possível concluir a autenticação. Tente novamente."));
  }, [router]);
  return <main><h1>Autenticação administrativa</h1><p role="status">{message}</p></main>;
}
