"use client";

import { useEffect, useRef, useState } from "react";

declare global {
  interface Window {
    turnstile?: {
      render: (container: HTMLElement, options: { sitekey: string; action: string; size: "compact"; callback: (token: string) => void; "error-callback": () => void; "expired-callback": () => void; "timeout-callback": () => void }) => string;
      reset: (widgetID: string) => void;
      remove: (widgetID: string) => void;
    };
  }
}

const scriptID = "cloudflare-turnstile-api";
const siteKey = process.env.NEXT_PUBLIC_TURNSTILE_SITE_KEY;

/** Renders a managed Turnstile challenge and keeps its one-time token in memory. */
export function TurnstileWidget({ onToken, resetKey }: { onToken: (token: string) => void; resetKey: number }) {
  const container = useRef<HTMLDivElement>(null);
  const widgetID = useRef<string | undefined>(undefined);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    onToken("");
    setError("");
    if (!siteKey) {
      setError("A verificação de segurança está indisponível. Tente novamente mais tarde.");
      return;
    }

    let disposed = false;
    const render = () => {
      if (disposed || !container.current || !window.turnstile) return;
      if (widgetID.current) window.turnstile.remove(widgetID.current);
      widgetID.current = window.turnstile.render(container.current, {
        sitekey: siteKey,
        action: "onboarding_otp",
        size: "compact",
        callback: (token) => { setError(""); onToken(token); },
        "error-callback": () => { onToken(""); setError("Não foi possível validar a verificação. Tente novamente."); },
        "expired-callback": () => { onToken(""); setError("A verificação expirou. Faça a verificação novamente."); },
        "timeout-callback": () => { onToken(""); setError("A verificação expirou. Faça a verificação novamente."); },
      });
    };

    if (window.turnstile) render();
    else {
      let script = document.getElementById(scriptID) as HTMLScriptElement | null;
      if (script?.dataset.loadFailed === "true") {
        script.remove();
        script = null;
      }
      if (!script) {
        script = document.createElement("script");
        script.id = scriptID;
        script.src = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
        script.async = true;
        script.defer = true;
        document.head.appendChild(script);
      }
      const activeScript = script;
      const onLoad = () => { activeScript.dataset.loaded = "true"; render(); };
      const onError = () => {
        activeScript.dataset.loadFailed = "true";
        activeScript.remove();
        if (!disposed) setError("Não foi possível carregar a verificação de segurança. Tente novamente.");
      };
      activeScript.addEventListener("load", onLoad, { once: true });
      activeScript.addEventListener("error", onError, { once: true });
      if (window.turnstile) render();
      else if (activeScript.dataset.loaded === "true") render();
    }
    return () => {
      disposed = true;
      if (widgetID.current && window.turnstile) window.turnstile.remove(widgetID.current);
      widgetID.current = undefined;
    };
  }, [attempt, onToken, resetKey]);

  return <div className="onboarding-turnstile" role="group" aria-label="Verificação de segurança">
    <div ref={container} />
    {error ? <><p role="alert" className="onboarding-turnstile-error">{error}</p>{siteKey ? <button type="button" onClick={() => setAttempt((value) => value + 1)}>Tentar novamente</button> : null}</> : null}
  </div>;
}
