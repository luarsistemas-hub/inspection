import React, { type SVGProps } from "react";

const paths: Record<string, React.ReactNode> = {
  LIVING_ROOM: <><path d="M4 12V8a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v4" /><path d="M3 12a2 2 0 0 1 2 2v3h14v-3a2 2 0 0 1 2-2v7H3z" /><path d="M6 19v2m12-2v2" /></>,
  KITCHEN: <><path d="M4 10h16v6a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4z" /><path d="M2 10h20M8 6V4m4 2V3m4 3V4" /></>,
  BEDROOM: <><path d="M3 12h18v7H3zM3 12V7m18 5V7M5 9h6v3H5zm8 0h6v3h-6zM3 19v2m18-2v2" /></>,
  BATHROOM: <><path d="M3 12h18v3a5 5 0 0 1-5 5H8a5 5 0 0 1-5-5zM5 12V6a3 3 0 0 1 6 0M7 20v2m10-2v2" /></>,
  SERVICE_AREA: <><rect x="4" y="3" width="16" height="19" rx="2" /><circle cx="12" cy="13" r="5" /><path d="M7 6h1m3 0h6" /></>,
  OTHER: <><path d="M12 4v16M4 12h16" /></>,
};

export function EnvironmentTypeIcon({ typeCode, ...props }: SVGProps<SVGSVGElement> & { typeCode: string }) {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" {...props}>{paths[typeCode] ?? <><path d="M3 21V7l9-4 9 4v14z" /><path d="M9 21v-7h6v7" /></>}</svg>;
}
