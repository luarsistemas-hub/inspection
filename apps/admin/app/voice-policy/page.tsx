import { Suspense } from "react";
import { AdminShell } from "@/features/admin/admin-shell";

export default function Page() {
  return <Suspense fallback={<main className="admin-denial">Carregando política de voz…</main>}><AdminShell section="Anotações por voz" /></Suspense>;
}
