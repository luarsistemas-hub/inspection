import "@inspection/design-system/styles.css";
import "./styles.css";
import type { Metadata } from "next";

export const metadata: Metadata = { title: "Inspection Administração", description: "Configuração e governança da imobiliária" };
export default function Layout({ children }: Readonly<{ children: React.ReactNode }>) { return <html lang="pt-BR"><body>{children}</body></html>; }
