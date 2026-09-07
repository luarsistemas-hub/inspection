"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { takePKCEVerifier } from "@/auth/pkce";
import { setAccessToken } from "@/auth/session";

export default function CallbackPage() {
  const [message, setMessage] = useState("Concluindo autenticação…");
  const router = useRouter();
  useEffect(() => {
    const query = new URLSearchParams(location.search);
    const code = query.get("code");
    const verifier = takePKCEVerifier(query.get("state"));
    if (!code || !verifier) { setMessage("Não foi possível validar a autenticação. Entre novamente."); return; }
    const endpoint = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL;
    if (!endpoint) { setMessage("Autenticação configurada para o ambiente de produção."); return; }
    void fetch(endpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "authorization_code", code, code_verifier: verifier, redirect_uri: `${location.origin}/auth/callback`, client_id: process.env.NEXT_PUBLIC_OIDC_CLIENT_ID ?? "inspection-web" }) })
      .then(async (response) => response.ok ? response.json() as Promise<{ access_token: string }> : Promise.reject())
      .then(({ access_token }) => {
        setAccessToken(access_token);
        // This is a client-side transition. The in-memory access token stays
        // alive for the tab and disappears naturally on a browser reload.
        router.replace("/");
      })
      .catch(() => setMessage("Não foi possível concluir a autenticação. Tente novamente."));
  }, [router]);
  return <main><h1>Autenticação interna</h1><p role="status">{message}</p></main>;
}
