import { Suspense } from "react";
import { AdminShell } from "@/features/admin/admin-shell";

export default function Page() {
  return <Suspense fallback={<main className="admin-denial">Carregando operações de vídeo…</main>}><AdminShell section="Operações de vídeo" /></Suspense>;
}
