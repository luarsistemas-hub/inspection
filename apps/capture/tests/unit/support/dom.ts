import { act, createElement, type ReactElement } from "react";
import { createRoot } from "react-dom/client";
import { vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const mounted: Array<{ root: ReturnType<typeof createRoot>; container: HTMLDivElement }> = [];

/** Renders an element into the document and returns its container. */
export function mount(element: ReactElement): { container: HTMLDivElement; rerender: (next: ReactElement) => void } {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  mounted.push({ root, container });
  act(() => root.render(element));
  return { container, rerender: (next) => act(() => root.render(next)) };
}

/** Unmounts everything rendered by `mount`, running effect cleanups. */
export function unmountAll(): void {
  for (const { root, container } of mounted.splice(0)) { act(() => root.unmount()); container.remove(); }
}

/** Lets promises, timers and fake-indexeddb requests finish inside act. */
export async function settle(rounds = 12): Promise<void> {
  for (let index = 0; index < rounds; index += 1) await act(async () => { await new Promise((resolve) => setTimeout(resolve, 5)); });
}

/** Polls until the condition holds, flushing React between attempts. */
export async function waitFor(condition: () => boolean, message = "condition", attempts = 120): Promise<void> {
  for (let index = 0; index < attempts; index += 1) {
    if (condition()) return;
    await settle(1);
  }
  throw new Error(`Timed out waiting for ${message}`);
}

/** Dispatches a pointer-like event React understands, with an id and type. */
export function pointer(target: Element | Window, type: "pointerdown" | "pointerup" | "pointercancel", id = 1, init: { button?: number; pointerType?: string } = {}): void {
  const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: init.button ?? 0 });
  Object.defineProperties(event, { pointerId: { value: id }, pointerType: { value: init.pointerType ?? "touch" } });
  act(() => { target.dispatchEvent(event); });
}

/** Dispatches a keyboard event; `repeat` models a held key. */
export function key(target: Element, type: "keydown" | "keyup", name: string, repeat = false): KeyboardEvent {
  const event = new KeyboardEvent(type, { key: name, bubbles: true, cancelable: true, repeat });
  act(() => { target.dispatchEvent(event); });
  return event;
}

export function click(target: Element, detail = 1): void {
  const event = new MouseEvent("click", { bubbles: true, cancelable: true, detail });
  act(() => { target.dispatchEvent(event); });
}

export function byText<T extends Element = HTMLElement>(container: ParentNode, selector: string, text: string | RegExp): T | undefined {
  return [...container.querySelectorAll<T>(selector)].find((element) => typeof text === "string" ? element.textContent?.includes(text) : text.test(element.textContent ?? ""));
}

export function button(container: ParentNode, name: string | RegExp): HTMLButtonElement | undefined {
  return [...container.querySelectorAll<HTMLButtonElement>("button")].find((element) => {
    const label = element.getAttribute("aria-label") ?? element.textContent ?? "";
    return typeof name === "string" ? label === name || element.textContent === name : name.test(label) || name.test(element.textContent ?? "");
  });
}

export function setValue(element: HTMLTextAreaElement | HTMLInputElement, value: string): void {
  const prototype = element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(prototype, "value")?.set;
  act(() => { setter?.call(element, value); element.dispatchEvent(new Event("input", { bubbles: true })); });
}

/** Async work started by the component (timers, promises) updates state between test steps; those updates are expected. */
export function silenceActWarnings(): void {
  const report = console.error.bind(console);
  vi.spyOn(console, "error").mockImplementation((...args: unknown[]) => { if (!String(args[0]).includes("not wrapped in act")) report(...args); });
}

export { createElement, vi };
