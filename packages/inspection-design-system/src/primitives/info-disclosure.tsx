"use client";

import { useId, useState, type ReactNode } from "react";
import { Button, Tooltip, TooltipTrigger } from "react-aria-components";
import { Icon } from "./icon.js";

export type InfoDisclosureProps = {
  /** Short name for the information, used to label the icon button. */
  label: string;
  /** Heading displayed beside the information button. */
  heading: ReactNode;
  children: ReactNode;
  className?: string;
};

/** Shows supplementary information on demand beside a heading. */
export function InfoDisclosure({ label, heading, children, className = "" }: InfoDisclosureProps) {
  const [open, setOpen] = useState(false);
  const panelId = useId();
  const accessibleLabel = `Informações sobre ${label}`;

  return <div className={`inspection-info-disclosure ${className}`.trim()}>
    <div className="inspection-info-disclosure__heading">
      {heading}
      <TooltipTrigger delay={500} closeDelay={0}>
        <Button
          aria-label={accessibleLabel}
          aria-expanded={open}
          aria-controls={panelId}
          className="inspection-info-disclosure__trigger"
          onPress={() => setOpen((current) => !current)}
        >
          <Icon name="info" size={20} />
        </Button>
        <Tooltip className="inspection-tooltip" placement="top">{accessibleLabel}</Tooltip>
      </TooltipTrigger>
    </div>
    <div
      id={panelId}
      className="inspection-info-disclosure__panel"
      data-open={open}
      aria-hidden={!open}
      inert={!open}
    >
      <div className="inspection-info-disclosure__panel-content">{children}</div>
    </div>
  </div>;
}
