import type { ReactNode } from "react";

export type PageHeaderProps = { title: string; description?: string; breadcrumbs?: ReactNode; actions?: ReactNode; emphasis?: "brand" | "tonal" | "plain"; id?: string };

/** Gives product pages a consistent heading, supporting copy, and action area. */
export function PageHeader({ title, description, breadcrumbs, actions, emphasis = "plain", id }: PageHeaderProps) {
  return <header className={`inspection-page-header inspection-page-header--${emphasis}`}>
    <div className="inspection-page-header__content">
      {breadcrumbs ? <div className="inspection-page-header__breadcrumb">{breadcrumbs}</div> : null}
      <h1 id={id}>{title}</h1>
      {description ? <p>{description}</p> : null}
    </div>
    {actions ? <div className="inspection-page-header__actions">{actions}</div> : null}
  </header>;
}
