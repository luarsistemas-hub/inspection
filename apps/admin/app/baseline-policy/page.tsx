import { AdminShell } from "@/features/admin/admin-shell";
import { Suspense } from "react";

export default function BaselinePolicyPage() {
  return <Suspense fallback={<main className="admin-denial">Carregando Administração…</main>}><AdminShell section="Política de inventário" /></Suspense>;
}
