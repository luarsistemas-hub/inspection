import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { EnvironmentDeclaration } from "../../app/capture/[linkToken]/environment-declaration";

vi.mock("@inspection/design-system", async () => {
  const React = await import("react");
  return {
    Button: ({ children, ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { isPending?: boolean; pendingLabel?: string }) => {
      const { isPending, pendingLabel, ...buttonProps } = props;
      void isPending;
      void pendingLabel;
      return React.createElement("button", buttonProps, children);
    },
    Field: ({ children, label, hint }: { children: React.ReactNode; label: string; hint?: string }) => React.createElement("label", null, label, children, hint && React.createElement("span", null, hint)),
    Input: (props: React.InputHTMLAttributes<HTMLInputElement>) => React.createElement("input", props),
    Select: (props: React.SelectHTMLAttributes<HTMLSelectElement>) => React.createElement("select", props)
  };
});

const roots: Array<{ root: ReturnType<typeof createRoot>; container: HTMLDivElement }> = [];
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
afterEach(() => {
  for (const { root, container } of roots.splice(0)) {
    act(() => root.unmount());
    container.remove();
  }
});

describe("EnvironmentDeclaration", () => {
  it("resets to an unused type without showing a duplicate error after declaring Sala", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    roots.push({ root, container });
    const onDeclare = vi.fn(async () => "env:sala");
    await act(async () => { root.render(createElement(EnvironmentDeclaration, { busy: false, onDeclare })); });

    const name = container.querySelector<HTMLInputElement>('input[aria-label="Nome do ambiente"]')!;
    const button = [...container.querySelectorAll("button")].find((item) => item.textContent === "Continuar")!;
    await act(async () => { button.dispatchEvent(new MouseEvent("click", { bubbles: true })); });

    expect(onDeclare).toHaveBeenCalledWith("LIVING_ROOM", "Sala de estar");
    expect(container.querySelector<HTMLSelectElement>("select")?.value).toBe("DINING_ROOM");
    expect(name.value).toBe("");
    expect(container.textContent).not.toContain("Esse ambiente já existe.");
    expect(button.disabled).toBe(false);
  });

  it("keeps a second name typed during the first declaration request", async () => {
    let finishDeclaration!: (value: string) => void;
    const onDeclare = vi.fn(() => new Promise<string>((resolve) => { finishDeclaration = resolve; }));
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    roots.push({ root, container });
    await act(async () => { root.render(createElement(EnvironmentDeclaration, { busy: false, onDeclare })); });

    const select = container.querySelector<HTMLSelectElement>("select")!;
    const name = container.querySelector<HTMLInputElement>('input[aria-label="Nome do ambiente"]')!;
    const button = [...container.querySelectorAll("button")].find((item) => item.textContent === "Continuar")!;
    const change = (element: HTMLInputElement | HTMLSelectElement, value: string, eventName: string) => {
      const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), "value")?.set;
      setter?.call(element, value);
      element.dispatchEvent(new Event(eventName, { bubbles: true }));
    };

    await act(async () => {
      change(select, "OTHER", "change");
      change(name, "Depósito", "input");
      button.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(onDeclare).toHaveBeenCalledWith("OTHER", "Depósito");
    expect(name.value).toBe("");
    expect(container.textContent).not.toContain("Informe o nome do ambiente.");

    await act(async () => { change(select, "KITCHEN", "change"); });
    await act(async () => { change(name, "Cozinha", "input"); });
    await act(async () => { finishDeclaration("env:sala"); await Promise.resolve(); });
    expect(name.value).toBe("Cozinha");
    expect(button.disabled).toBe(false);
  });
});
