"use client";

import { Button, Tooltip, TooltipTrigger } from "react-aria-components";
import { Icon, type IconName } from "./icon.js";

export type IconButtonProps = {
  label: string;
  tooltip?: string;
  icon: IconName;
  onPress: () => void;
  className?: string;
  isDisabled?: boolean;
};

/** Renders a compact icon action with an accessible name and pointer/keyboard tooltip. */
export function IconButton({ label, tooltip = label, icon, onPress, className = "", isDisabled = false }: IconButtonProps) {
  return <TooltipTrigger delay={500} closeDelay={0}>
    <Button aria-label={label} className={`inspection-icon-button ${className}`.trim()} isDisabled={isDisabled} onPress={onPress}>
      <Icon name={icon} size={24} />
    </Button>
    <Tooltip className="inspection-tooltip" placement="top">{tooltip}</Tooltip>
  </TooltipTrigger>;
}
