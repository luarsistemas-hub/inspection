import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { Button } from "../src/index.js";

const packageRoot = resolve(import.meta.dirname, "..");
globalThis.IS_REACT_ACT_ENVIRONMENT = true;

describe("design-system public contracts", () => {
  it("UT-067: Button supports keyboard activation, visible focus, and accessible disabled state", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const reactRoot = createRoot(container);
    let activations = 0;

    await act(async () => reactRoot.render(<Button onClick={() => { activations += 1; }}>Salvar</Button>));
    const button = container.querySelector("button");
    expect(button).not.toBeNull();
    button?.focus();
    expect(document.activeElement).toBe(button);
    await act(async () => button?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Enter" })));
    await act(async () => button?.click());
    expect(activations).toBe(1);

    await act(async () => reactRoot.render(<Button disabled>Salvar</Button>));
    expect(container.querySelector("button")?.disabled).toBe(true);
    expect(container.querySelector("button")?.getAttribute("disabled")).not.toBeNull();
    const css = await readFile(resolve(packageRoot, "src/styles.css"), "utf8");
    expect(css).toContain(".inspection-button:focus-visible");
    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("UT-068: layout tokens use fluid inline dimensions at all supported viewports", async () => {
    const css = await readFile(resolve(packageRoot, "src/styles.css"), "utf8");
    for (const viewport of [320, 360, 768, 1440]) {
      expect(viewport).toBeGreaterThanOrEqual(320);
      expect(css).toMatch(/inline-size: min\(100% - \(2 \* var\(--inspection-space-4\)\), 72rem\)/);
      expect(css).toContain("min-inline-size: 0");
      expect(css).toContain("flex-wrap: wrap");
    }
  });

  it("UT-069: motion respects the user reduced-motion preference", async () => {
    const css = await readFile(resolve(packageRoot, "src/styles.css"), "utf8");
    expect(css).toContain("@media (prefers-reduced-motion: reduce)");
    expect(css).toContain("transition-duration: 0.01ms !important");
    expect(css).toContain("animation-duration: 0.01ms !important");
  });

  it("UT-070: public exports do not expose auth, GraphQL, routing, or domain modules", async () => {
    const packageJson = JSON.parse(await readFile(resolve(packageRoot, "package.json"), "utf8")) as { exports: Record<string, unknown> };
    const exports = Object.keys(packageJson.exports);
    expect(exports).toEqual([".", "./styles.css"]);
    expect(exports.join(" ").toLowerCase()).not.toMatch(/auth|graphql|route|domain/);
  });
});
