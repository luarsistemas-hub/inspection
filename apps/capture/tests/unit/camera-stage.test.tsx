import React, { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CameraHint, CameraShutter, CameraStage, CameraThumbnail } from "../../app/capture/[linkToken]/camera-stage";

Object.defineProperty(globalThis, "IS_REACT_ACT_ENVIRONMENT", { configurable: true, value: true });

describe("CameraStage", () => {
  let host: HTMLDivElement;
  let root: Root;

  const render = (element: React.ReactNode) => {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    act(() => root.render(element));
    return document.body.querySelector<HTMLElement>(".camera-stage")!;
  };

  afterEach(() => {
    if (root) act(() => root.unmount());
    host?.remove();
    document.body.style.overflow = "";
  });

  const baseProps = { label: "Câmera · Cozinha", backLabel: "Ambientes", title: "Cozinha", media: <video />, center: <button type="button">Disparar</button> };

  it("renders an accessible modal dialog, conditionally showing back", () => {
    const stage = render(<CameraStage {...baseProps} onBack={() => undefined} />);
    expect(stage.getAttribute("role")).toBe("dialog");
    expect(stage.getAttribute("aria-modal")).toBe("true");
    expect(stage.querySelector(".camera-stage__back")).toBeTruthy();
    act(() => root.render(<CameraStage {...baseProps} />));
    expect(document.body.querySelector(".camera-stage__back")).toBeNull();
  });

  it("routes Escape to back or the open sheet", () => {
    const back = vi.fn();
    let stage = render(<CameraStage {...baseProps} onBack={back} />);
    act(() => stage.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(back).toHaveBeenCalledOnce();

    const close = vi.fn();
    act(() => root.render(<CameraStage {...baseProps} onBack={back} sheet={{ title: "Ajuda", content: <button>Entendi</button>, onClose: close }} />));
    stage = document.body.querySelector<HTMLElement>(".camera-stage")!;
    act(() => stage.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(close).toHaveBeenCalledOnce();
    expect(back).toHaveBeenCalledOnce();
  });

  it("labels current and completed chips accessibly", () => {
    const stage = render(<CameraStage {...baseProps} strip={{ label: "Ambientes", items: [{ key: "k", label: "Cozinha", done: true, current: true, detail: "1/3" }] }} />);
    const chip = stage.querySelector(".camera-stage__chip")!;
    expect(chip.getAttribute("aria-current")).toBe("step");
    expect(chip.getAttribute("aria-label")).toBe("Cozinha, concluído, 1/3");
  });

  it("renders a labelled recording shutter with a progress ring", () => {
    const hostNode = document.createElement("div");
    const testRoot = createRoot(hostNode);
    act(() => testRoot.render(<CameraShutter mode="recording" label="Parar gravação" onPress={() => undefined} progress={0.4} />));
    expect(hostNode.querySelector("button")?.getAttribute("aria-label")).toBe("Parar gravação");
    expect(hostNode.querySelector(".camera-stage__progress circle")).toBeTruthy();
    act(() => testRoot.unmount());
  });

  it("keeps an empty thumbnail decorative and labels a populated count", () => {
    const hostNode = document.createElement("div");
    const testRoot = createRoot(hostNode);
    act(() => testRoot.render(<><CameraThumbnail count={0} label="Última captura" /><CameraThumbnail url="foto.jpg" count={2} label="Duas capturas" /></>));
    expect(hostNode.querySelector(".camera-stage__thumbnail-empty")?.getAttribute("aria-hidden")).toBe("true");
    expect(hostNode.querySelectorAll(".camera-stage__thumbnail-count")).toHaveLength(1);
    expect(hostNode.querySelector(".camera-stage__thumbnail-count")?.textContent).toBe("2");
    expect(hostNode.querySelectorAll(".camera-stage__thumbnail")[1].getAttribute("aria-label")).toBe("Duas capturas");
    act(() => testRoot.unmount());
  });

  it("toggles the hint card and compact pill", () => {
    const toggle = vi.fn();
    const hostNode = document.createElement("div");
    const testRoot = createRoot(hostNode);
    act(() => testRoot.render(<><CameraHint title="Enquadramento" text="Centralize o ambiente." open onToggle={toggle} /><CameraHint title="Enquadramento" text="Centralize o ambiente." open={false} onToggle={toggle} /></>));
    expect(hostNode.querySelector(".camera-stage__hint strong")?.textContent).toBe("Enquadramento");
    expect(hostNode.querySelector(".camera-stage__hint-pill")?.getAttribute("aria-label")).toBe("Mostrar dica");
    act(() => hostNode.querySelector<HTMLButtonElement>(".camera-stage__hint button")!.click());
    expect(toggle).toHaveBeenCalledWith(false);
    act(() => testRoot.unmount());
  });

  it("sets aspect ratio and restores modal page state and focus", () => {
    const trigger = document.createElement("button");
    document.body.append(trigger);
    trigger.focus();
    const sibling = document.createElement("div");
    document.body.append(sibling);
    const stage = render(<CameraStage {...baseProps} aspectRatio={16 / 9} />);
    expect(stage.querySelector(".camera-stage__viewfinder")?.getAttribute("style")).toContain("--camera-aspect: 1.777");
    expect(document.documentElement.classList.contains("camera-stage-open")).toBe(true);
    expect(document.activeElement).toBe(stage);
    expect(sibling.hasAttribute("inert")).toBe(true);
    act(() => root.unmount());
    expect(document.documentElement.classList.contains("camera-stage-open")).toBe(false);
    expect(sibling.hasAttribute("inert")).toBe(false);
    expect(document.activeElement).toBe(trigger);
    sibling.remove();
    trigger.remove();
  });
});
