import { AdminShell } from "@/features/admin/admin-shell";
import { Suspense } from "react";

export default function Page() {
  return <Suspense fallback={<main className="admin-denial">Carregando Consumo de LLM…</main>}><AdminShell section="Consumo de LLM" /></Suspense>;
}
