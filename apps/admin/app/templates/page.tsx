import { Suspense } from "react";
import { AdminShell } from "@/features/admin/admin-shell";

export default function Page() {
  return <Suspense fallback={<main className="admin-denial">Carregando modelos de vistoria…</main>}><AdminShell section="Modelos de vistoria" /></Suspense>;
}
