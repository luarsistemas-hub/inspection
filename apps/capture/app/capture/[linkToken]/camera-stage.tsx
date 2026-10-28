"use client";

import React, { useCallback, useEffect, useLayoutEffect, useRef } from "react";
import { createPortal } from "react-dom";

/** One chip in the horizontal item strip. */
export type CameraStripItem = { key: string; label: string; done: boolean; current: boolean; detail?: string };

/** Local SVG glyphs available to the camera interface. */
export type CameraGlyphName = "back" | "help" | "gallery" | "upload" | "offline" | "crop" | "info" | "close" | "check" | "tips";

/** Draws one camera-interface glyph without loading an icon dependency. */
export function CameraGlyph({ name }: { name: CameraGlyphName }): React.JSX.Element {
  const shared = { fill: "none", stroke: "currentColor", strokeWidth: 2, strokeLinecap: "round" as const, strokeLinejoin: "round" as const };
  const paths: Record<CameraGlyphName, React.ReactNode> = {
    back: <path d="m15 18-6-6 6-6" />,
    help: <><circle cx="12" cy="12" r="9" /><path d="M9.6 9a2.5 2.5 0 1 1 4.3 1.7c-1 .9-1.9 1.2-1.9 2.8M12 17h.01" /></>,
    gallery: <><rect x="3" y="4" width="18" height="16" rx="2" /><circle cx="8.5" cy="9" r="1.5" /><path d="m21 15-5-5L5 20" /></>,
    upload: <><path d="M12 16V4m-4 4 4-4 4 4" /><path d="M5 14v5a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-5" /></>,
    offline: <><path d="M3 3l18 18M8.5 8.5a6 6 0 0 1 8.1.5M5 12a10 10 0 0 1 2-2.2M2 8a15 15 0 0 1 4-3M12 20h.01" /><path d="M16 16a5 5 0 0 1 2 2" /></>,
    crop: <><path d="M6 3v12a3 3 0 0 0 3 3h12M18 21V9a3 3 0 0 0-3-3H3" /></>,
    info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v5m0-8h.01" /></>,
    close: <path d="m18 6-12 12M6 6l12 12" />,
    check: <path d="m5 12 4 4L19 6" />,
    tips: <><path d="M9 18h6m-5 3h4M8.5 14.5a6 6 0 1 1 7 0c-.8.6-1.2 1.3-1.3 2.5h-4.4c-.1-1.2-.5-1.9-1.3-2.5Z" /><path d="M12 2v1m8 3-.8.6M4 6l.8.6" /></>,
  };
  return <svg width="24" height="24" viewBox="0 0 24 24" aria-hidden="true" {...shared}>{paths[name]}</svg>;
}

/** Full-screen dark camera layout used by photo and video capture. */
export function CameraStage(props: {
  label: string; backLabel: string; onBack?: () => void; title: string; subtitle?: string; statusPill?: React.ReactNode;
  onHelp?: () => void; aspectRatio?: number; media: React.ReactNode; overlayTop?: React.ReactNode; overlayBottom?: React.ReactNode;
  areaLabel?: string; areaLabelVisibleWithOverlay?: boolean; strip?: { label: string; items: CameraStripItem[]; onSelect?: (key: string) => void; disabled?: boolean };
  showGuides?: boolean;
  left?: React.ReactNode; center: React.ReactNode; caption?: string; right?: React.ReactNode;
  sheet?: { title: string; content: React.ReactNode; onClose?: () => void }; notice?: React.ReactNode; liveMessage?: string; liveRegion?: React.ReactNode;
}): React.JSX.Element | null {
  const stageRef = useRef<HTMLElement>(null);
  const frameRef = useRef<HTMLDivElement>(null);
  const priorFocusRef = useRef<HTMLElement | null>(null);
  const stripRef = useRef<HTMLDivElement>(null);
  const sheetRef = useRef<HTMLDivElement>(null);
  const aspect = props.aspectRatio ?? 0.75;

  useLayoutEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    priorFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const siblings = [...document.body.children].filter((child) => child !== stage).map((child) => {
      const element = child as HTMLElement;
      return { element, inert: element.inert, inertAttribute: element.hasAttribute("inert"), ariaHidden: element.getAttribute("aria-hidden") };
    });
    const priorOverflow = document.body.style.overflow;
    document.documentElement.classList.add("camera-stage-open");
    document.body.style.overflow = "hidden";
    siblings.forEach(({ element }) => { element.inert = true; element.setAttribute("inert", ""); element.setAttribute("aria-hidden", "true"); });
    stage.focus();
    return () => {
      document.documentElement.classList.remove("camera-stage-open");
      document.body.style.overflow = priorOverflow;
      siblings.forEach(({ element, inert, inertAttribute, ariaHidden }) => {
        element.inert = inert;
        if (inertAttribute) element.setAttribute("inert", "");
        else element.removeAttribute("inert");
        if (ariaHidden === null) element.removeAttribute("aria-hidden");
        else element.setAttribute("aria-hidden", ariaHidden);
      });
      if (priorFocusRef.current?.isConnected) priorFocusRef.current.focus();
    };
  }, []);

  useLayoutEffect(() => {
    if (!props.sheet) return;
    sheetRef.current?.querySelector<HTMLElement>("button:not(:disabled), [href], input:not(:disabled), [tabindex]:not([tabindex='-1'])")?.focus();
  }, [props.sheet]);

  useEffect(() => {
    const current = stripRef.current?.querySelector<HTMLElement>("[aria-current='step']");
    try { current?.scrollIntoView?.({ inline: "center", block: "nearest" }); } catch { /* jsdom does not implement scrolling */ }
  }, [props.strip?.items]);

  const onKeyDown = useCallback((event: React.KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape") {
      if (props.sheet) props.sheet.onClose?.();
      else props.onBack?.();
      return;
    }
    if (event.key !== "Tab") return;
    const scope = props.sheet ? sheetRef.current : stageRef.current;
    if (!scope) return;
    const focusable = [...scope.querySelectorAll<HTMLElement>("button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex='-1'])")]
      .filter((element) => element.getAttribute("aria-hidden") !== "true" && !element.hasAttribute("hidden"));
    if (focusable.length === 0) { event.preventDefault(); scope.focus(); return; }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && (document.activeElement === first || !scope.contains(document.activeElement))) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && (document.activeElement === last || !scope.contains(document.activeElement))) { event.preventDefault(); first.focus(); }
  }, [props.onBack, props.sheet]);

  if (typeof document === "undefined") return null;
  return createPortal(
    <section ref={stageRef} className="camera-stage" role="dialog" aria-modal="true" aria-label={props.label} tabIndex={-1} onKeyDown={onKeyDown}>
      <header className="camera-stage__topbar">
        <div className="camera-stage__topbar-start">
          {props.onBack && <button className="camera-stage__glass camera-stage__back" type="button" aria-label={props.backLabel} onClick={props.onBack}><CameraGlyph name="back" /><span>{props.backLabel}</span></button>}
        </div>
        <div className="camera-stage__heading-wrap">
          <h1 className="camera-stage__heading"><span>{props.title}</span>{props.subtitle && <small>{props.subtitle}</small>}</h1>
          {props.statusPill && <div className="camera-stage__status-pill">{props.statusPill}</div>}
        </div>
        <div className="camera-stage__topbar-end">{props.onHelp && <button className="camera-stage__glass camera-stage__help" type="button" aria-label="Ajuda" onClick={props.onHelp}><CameraGlyph name="help" /></button>}</div>
      </header>
      <div className="camera-stage__main">
        <div className="camera-stage__frame" ref={frameRef}>
          <div className="camera-stage__viewfinder" style={{ "--camera-aspect": aspect } as React.CSSProperties}>
            {React.isValidElement(props.media) ? React.cloneElement(props.media as React.ReactElement<{ className?: string }>, { className: `camera-stage__media ${(props.media.props as { className?: string }).className ?? ""}`.trim() }) : props.media}
            {props.showGuides !== false && <div className="camera-stage__grid" aria-hidden="true">{Array.from({ length: 9 }, (_, index) => <i key={index} />)}</div>}
            {props.overlayTop && <div className="camera-stage__overlay-top">{props.overlayTop}</div>}
            {props.showGuides !== false && props.areaLabel && (!props.overlayBottom || props.areaLabelVisibleWithOverlay) && <span className="camera-stage__area-label"><CameraGlyph name="crop" />{props.areaLabel}</span>}
            {props.overlayBottom && <div className="camera-stage__overlay-bottom">{props.overlayBottom}</div>}
          </div>
        </div>
        {props.notice && <div className="camera-stage__notice">{props.notice}</div>}
        {props.strip && <div className="camera-stage__strip" role="group" aria-label={props.strip.label} ref={stripRef}>
          {props.strip.items.map((item) => <button key={item.key} type="button" className={`camera-stage__chip${item.current ? " is-current" : ""}${item.done ? " is-done" : ""}`} aria-current={item.current ? "step" : undefined} aria-label={item.done ? `${item.label}, concluído${item.detail ? `, ${item.detail}` : ""}` : item.detail ? `${item.label}, ${item.detail}` : item.label} disabled={props.strip?.disabled} onClick={() => props.strip?.onSelect?.(item.key)}>
            <span className="camera-stage__chip-visual">{item.done && <CameraGlyph name="check" />}<span>{item.label}</span>{item.detail && <small>{item.detail}</small>}</span>
          </button>)}
        </div>}
      </div>
      <footer className="camera-stage__controls">
        <div className="camera-stage__control-side camera-stage__control-side--left">{props.left}</div>
        <div className="camera-stage__control-center">{props.center}{props.caption && <span className="camera-stage__caption">{props.caption}</span>}</div>
        <div className="camera-stage__control-side camera-stage__control-side--right">{props.right}</div>
      </footer>
      {props.liveMessage && <div className="camera-stage__live" aria-live="polite">{props.liveMessage}</div>}
      {props.liveRegion}
      {props.sheet && <div className="camera-stage__sheet-layer">
        <button className="camera-stage__scrim" type="button" aria-label="Fechar painel" onClick={() => props.sheet?.onClose?.()} />
        <CameraSheet ref={sheetRef} title={props.sheet.title} onClose={props.sheet.onClose}>{props.sheet.content}</CameraSheet>
      </div>}
    </section>, document.body,
  );
}

/** Big round shutter. */
export function CameraShutter({ mode, label, onPress, disabled, progress = 0 }: { mode: "photo" | "video" | "recording"; label: string; onPress: () => void; disabled?: boolean; progress?: number }): React.JSX.Element {
  const boundedProgress = Math.min(1, Math.max(0, progress));
  return <button type="button" className={`camera-stage__shutter is-${mode}`} aria-label={label} onClick={onPress} disabled={disabled}>
    {mode === "recording" && <svg className="camera-stage__progress" viewBox="0 0 100 100" aria-hidden="true"><circle cx="50" cy="50" r="44" pathLength="1" style={{ strokeDasharray: `${boundedProgress} 1` }} /></svg>}
    <span className="camera-stage__shutter-disc" />
  </button>;
}

/** Last-capture thumbnail with count badge. */
export function CameraThumbnail({ url, count, label, onPress, busy, offline }: { url?: string; count: number; label: string; onPress?: () => void; busy?: boolean; offline?: boolean }): React.JSX.Element {
  const content = <>{url ? <img src={url} alt="" /> : <span className="camera-stage__thumbnail-empty" aria-hidden="true" />}{count > 0 && <span className="camera-stage__thumbnail-count">{count}</span>}{(busy || offline) && <span className="camera-stage__thumbnail-state" aria-hidden="true"><CameraGlyph name={offline ? "offline" : "upload"} /></span>}</>;
  return onPress ? <button type="button" className="camera-stage__thumbnail" aria-label={label} onClick={onPress} disabled={busy}>{content}</button> : <span className="camera-stage__thumbnail" role="img" aria-label={label}>{content}</span>;
}

/** Square glass icon button with a caption below. */
export function CameraSideButton({ icon, label, onPress, disabled }: { icon: CameraGlyphName; label: string; onPress: () => void; disabled?: boolean }): React.JSX.Element {
  return <button className="camera-stage__side-button" type="button" onClick={onPress} disabled={disabled}><span className="camera-stage__side-icon"><CameraGlyph name={icon} /></span><span>{label}</span></button>;
}

/** Dismissible hint card shown over the viewfinder. */
export function CameraHint({ title, text, open, onToggle }: { title: string; text: string; open: boolean; onToggle: (open: boolean) => void }): React.JSX.Element {
  return open ? <div className="camera-stage__hint"><CameraGlyph name="tips" /><div><strong>{title}</strong><p>{text}</p></div><button type="button" aria-label="Ocultar dica" onClick={() => onToggle(false)}><CameraGlyph name="close" /></button></div>
    : <button type="button" className="camera-stage__hint-pill" aria-label="Mostrar dica" onClick={() => onToggle(true)}><CameraGlyph name="info" /><span>{title}</span></button>;
}

/** Toast over the bottom of the viewfinder. */
export function CameraToast({ imageUrl, title, text, tone = "success", action, onDismiss }: { imageUrl?: string; title: string; text?: string; tone?: "success" | "warning"; action?: { label: string; onPress: () => void }; onDismiss?: () => void }): React.JSX.Element {
  return <div className={`camera-stage__toast is-${tone}`} role="status">{imageUrl ? <img src={imageUrl} alt="" /> : <span className="camera-stage__toast-icon"><CameraGlyph name={tone === "success" ? "check" : "info"} /></span>}<div className="camera-stage__toast-copy"><strong>{title}</strong>{text && <span>{text}</span>}</div>{action && <button type="button" onClick={action.onPress}>{action.label}</button>}{onDismiss && <button type="button" aria-label="Fechar aviso" onClick={onDismiss}><CameraGlyph name="close" /></button>}</div>;
}

/** Bottom sheet inside the stage. */
export const CameraSheet = React.forwardRef<HTMLDivElement, { title: string; children: React.ReactNode; onClose?: () => void }>(function CameraSheet({ title, children, onClose }, ref): React.JSX.Element {
  return <section className="camera-stage__sheet" role="region" aria-label={title} ref={ref} tabIndex={-1} onClick={(event) => event.stopPropagation()}>
    <div className="camera-stage__sheet-handle" aria-hidden="true" /><div className="camera-stage__sheet-heading"><h2>{title}</h2>{onClose && <button type="button" aria-label="Fechar painel" onClick={onClose}><CameraGlyph name="close" /></button>}</div><div className="camera-stage__sheet-content">{children}</div>
  </section>;
});
