"use client";

import { useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";

import { print } from "graphql";
import { VerifyReportDocument } from "@/graphql/generated";

type Verification = { reportNumber?: string | null; version?: number | null; digest?: string | null; status?: string | null; issuedAt?: string | null };
const verificationQuery = printVerifyReportDocument();
function printVerifyReportDocument() { return print(VerifyReportDocument); }

export function formatIssuedAt(value?: string | null) {
  if (!value) return "Data indisponível";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Data indisponível" : new Intl.DateTimeFormat("pt-BR", { dateStyle: "long", timeStyle: "short" }).format(date);
}

/** PublicReportVerification confirms the identity of an issued report without loading its private contents. */
export function PublicReportVerification() {
  const params = useSearchParams();
  const code = params.get("code") ?? "";
  const versionValue = Number(params.get("version"));
  const [state, setState] = useState<{ heading: string; message: string; details?: Verification }>(() => ({ heading: "Verificação pendente", message: "Informe um código e a versão do laudo para verificar." }));
  const [copyMessage, setCopyMessage] = useState("");
  useEffect(() => {
    let active = true;
    if (!code || !Number.isInteger(versionValue) || versionValue < 1) {
      setState({ heading: "Dados de verificação inválidos", message: "Confira o link ou o código impresso no laudo." });
      return () => { active = false; };
    }
    setState({ heading: "Verificando laudo…", message: "Aguarde enquanto confirmamos a versão informada." });
    const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";
    void fetch(endpoint, { method: "POST", credentials: "omit", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ query: verificationQuery, variables: { code, version: versionValue } }) })
      .then(async (response) => {
        const body = await response.json() as { data?: { verifyReport?: Verification }; errors?: Array<{ message?: string; extensions?: { code?: string } }> };
        if (!response.ok || body.errors?.length) {
          const rateLimited = body.errors?.some((error) => error.extensions?.code === "RATE_LIMITED" || error.message === "Muitas consultas de verificação. Aguarde um instante e tente novamente.");
          if (active) setState(rateLimited ? { heading: "Limite de consultas atingido", message: "Foram feitas muitas consultas em pouco tempo. Aguarde um instante e tente novamente." } : { heading: "Não foi possível verificar agora", message: "Tente novamente mais tarde." });
          return;
        }
        const result = body.data?.verifyReport;
        if (!result?.reportNumber || !result.version || !result.digest) {
          if (active) setState({ heading: "Laudo não encontrado", message: "O código ou a versão informada não corresponde a um laudo disponível." });
          return;
        }
        const status = result.status?.toUpperCase();
        const superseded = status === "SUPERSEDED" || status === "REPLACED";
        if (active) setState({ heading: superseded ? "Versão substituída" : status === "PUBLISHED" ? "Laudo válido" : "Laudo não válido", message: superseded ? "Esta versão foi substituída por uma emissão posterior. Consulte a versão mais recente com quem enviou o laudo." : status === "PUBLISHED" ? "A identificação e a integridade desta versão foram confirmadas." : "Esta versão não está publicada como vigente.", details: result });
      })
      .catch(() => { if (active) setState({ heading: "Não foi possível verificar agora", message: "Confira sua conexão e tente novamente mais tarde." }); });
    return () => { active = false; };
  }, [code, versionValue]);
  const copyDigest = async () => {
    if (!state.details?.digest) return;
    try { await navigator.clipboard.writeText(state.details.digest); setCopyMessage("Resumo de integridade copiado."); }
    catch { setCopyMessage("Não foi possível copiar o resumo de integridade."); }
  };
  return <section aria-labelledby="verification-heading"><p className="report-kicker">Verificação pública de laudo</p><h1 id="verification-heading">{state.heading}</h1><p role="status">{state.message}</p>{state.details && <dl><div><dt>Número do laudo</dt><dd>{state.details.reportNumber}</dd></div><div><dt>Versão</dt><dd>{state.details.version}</dd></div><div><dt>Emitido em</dt><dd>{formatIssuedAt(state.details.issuedAt)}</dd></div><div><dt>Resumo de integridade (SHA-256)</dt><dd><code>{state.details.digest}</code> <button type="button" className="secondary" onClick={() => void copyDigest()}>Copiar resumo</button></dd></div></dl>}{copyMessage && <p role="status">{copyMessage}</p>}<p>Esta consulta confirma a versão do documento e não libera acesso às evidências ou aos dados da vistoria.</p></section>;
}
