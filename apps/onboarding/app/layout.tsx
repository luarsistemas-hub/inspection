import type { Metadata } from "next";
import { ThemeProvider, ThemeScript } from "@inspection/design-system";
import "@inspection/design-system/styles.css";
import "./onboarding.css";

export const metadata: Metadata = { title: "Começar vistoria", description: "Cadastro inicial para vistorias imobiliárias." };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="pt-BR" suppressHydrationWarning><head><meta name="theme-color" content="#f5f7ff"/><ThemeScript /></head><body><ThemeProvider>{children}</ThemeProvider></body></html>;
}
