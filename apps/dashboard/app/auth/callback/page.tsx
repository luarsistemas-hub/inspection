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
    const endpoint = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL;
    if (!endpoint) { setMessage("A autenticação não está configurada neste ambiente."); return; }
    void fetch(endpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, code_verifier: attempt.verifier, redirect_uri: `${location.origin}/auth/callback`, client_id: "inspection-dashboard" }) })
      .then((response) => response.ok ? response.json() as Promise<{ access_token: string }> : Promise.reject())
      .then(({ access_token }) => { setSession(access_token); router.replace(attempt.returnTo); })
      .catch(() => setMessage("Não foi possível concluir a autenticação. Tente novamente."));
  }, [router]);
  return <main className="denial"><h1>Autenticação do Dashboard</h1><p role="status">{message}</p></main>;
}
