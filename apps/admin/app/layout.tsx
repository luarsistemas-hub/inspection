import "@inspection/design-system/styles.css";
import "./styles.css";
import type { Metadata } from "next";
import { ThemeProvider, ThemeScript } from "@inspection/design-system";

export const metadata: Metadata = { title: "Inspection Administração", description: "Configuração e governança da imobiliária" };
export default function Layout({ children }: Readonly<{ children: React.ReactNode }>) { return <html lang="pt-BR" suppressHydrationWarning><head><meta name="theme-color" content="#f5f7ff"/><ThemeScript /></head><body><ThemeProvider>{children}</ThemeProvider></body></html>; }
