import { createElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { VideoCapture } from "../../app/capture/[linkToken]/video-capture";

vi.mock("@inspection/design-system", async () => {
  const React = await import("react");
  const wrap = (tag: string) => ({ children, ...props }: { children?: ReactNode; [key: string]: unknown }) => React.createElement(tag, props, children);
  return { Alert: wrap("div"), Button: wrap("button"), Card: wrap("section"), Field: wrap("div"), ProductIdentity: () => null, Select: wrap("select"), Status: wrap("span"), Textarea: wrap("textarea"), ThemeSelector: () => null };
});

describe("video recapture projection", () => {
  it("shows selected component, reason, deadline and accessible reference frame", () => {
    const markup = renderToStaticMarkup(createElement(VideoCapture, {
      responsibilityId: "responsibility-1",
      kind: "RECAPTURE",
      recaptureRequestId: "request-1",
      recaptureDeadlineAt: "2026-10-18T12:00:00Z",
      policy: {},
      requirements: [],
      answers: [],
      environments: [],
      comparisonMode: "FIXED_ORIGIN",
      online: true,
      onSessionFailure: () => false,
      referenceItems: [{
        mediaId: "frame-1", evidenceId: "frame-1", sourceMediaId: "media-1", timestampMs: 1250,
        environment: "Cozinha", component: "Pia", view: "Frente", ambiguous: false, gap: false,
        requirementKey: "video:component-1", description: "Cozinha · Pia", reason: "A pia está opaca",
        availability: "AVAILABLE", imageUrl: "/media/video-evidence/frame-1?responsibilityId=responsibility-1", imageUrlExpiresAt: null
      }]
    }));

    expect(markup).toContain("Itens a complementar");
    expect(markup).toContain("Cozinha · Pia");
    expect(markup).toContain("Motivo: A pia está opaca");
    expect(markup).toContain("Prazo:");
    expect(markup).toContain("2026");
    expect(markup).toContain('alt="Imagem de referência: Cozinha · Pia"');
    expect(markup).toContain("/media/video-evidence/frame-1?responsibilityId=responsibility-1");
    expect(markup).not.toContain("Sem comparação histórica nesta captura");
  });
});
