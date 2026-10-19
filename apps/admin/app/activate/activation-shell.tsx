import type { ReactNode } from "react";

type ActivationShellProps = {
  title: string;
  description: string;
  children: ReactNode;
};

export function ActivationShell({ title, description, children }: ActivationShellProps) {
  return (
    <main className="admin-denial admin-activation">
      <aside className="admin-gate-story">
        <div className="admin-gate-story-brand">
          <span className="admin-brand-mark" aria-hidden="true">I</span>
          <span>Inspection</span>
        </div>
        <div>
          <h2>Cada vistoria, com clareza.</h2>
          <p>Planeje vistorias e reúna evidências para decidir com segurança.</p>
        </div>
        <small>Administração</small>
      </aside>
      <section className="admin-gate-form" aria-labelledby="activation-title">
        <span className="admin-gate-mobile-brand">Inspection</span>
        <h1 id="activation-title">{title}</h1>
        <p>{description}</p>
        {children}
        <small>Português (Brasil) · Acesso seguro</small>
      </section>
    </main>
  );
}
