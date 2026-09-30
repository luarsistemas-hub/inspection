import type { ReactNode } from "react";
import { InfoDisclosure } from "./info-disclosure.js";

export type PageHeaderProps = { title: string; description?: string; descriptionMode?: "inline" | "disclosure"; breadcrumbs?: ReactNode; actions?: ReactNode; emphasis?: "brand" | "tonal" | "plain"; id?: string };

/** Gives product pages a consistent heading, supporting copy, and action area. */
export function PageHeader({ title, description, descriptionMode = "inline", breadcrumbs, actions, emphasis = "plain", id }: PageHeaderProps) {
  return <header className={`inspection-page-header inspection-page-header--${emphasis}`}>
    <div className="inspection-page-header__content">
      {breadcrumbs ? <div className="inspection-page-header__breadcrumb">{breadcrumbs}</div> : null}
      {description && descriptionMode === "disclosure"
        ? <InfoDisclosure className="inspection-page-header__disclosure" label={title} heading={<h1 id={id}>{title}</h1>}>{description}</InfoDisclosure>
        : <><h1 id={id}>{title}</h1>{description ? <p>{description}</p> : null}</>}
    </div>
    {actions ? <div className="inspection-page-header__actions">{actions}</div> : null}
  </header>;
}
