import { Suspense } from "react";
import { AdminShell } from "@/features/admin/admin-shell";

export default function Page() {
  return <Suspense fallback={<main className="admin-denial">Carregando Administração…</main>}><AdminShell section="Prompts de análise" /></Suspense>;
}
