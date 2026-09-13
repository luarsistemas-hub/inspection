import "@inspection/design-system/styles.css";
import "./styles.css";
import type { Metadata } from "next";

export const metadata: Metadata = { title: "Inspection Painel", description: "Operações e acompanhamento de vistorias" };
export default function Layout({ children }: Readonly<{ children: React.ReactNode }>) { return <html lang="pt-BR"><body>{children}</body></html>; }
