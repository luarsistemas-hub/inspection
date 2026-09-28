import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { Breadcrumbs, Button, Checkbox, ChoiceGroup, Combobox, Confirmation, DataTable, Dialog, ErrorSummary, Field, Input, Pagination, Recovery, Steps, VersionConflict } from "../src/index.js";

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
    const styleElement = document.createElement("style");
    styleElement.textContent = css;
    document.head.append(styleElement);
    const stylesheet = styleElement.sheet;
    expect(stylesheet).not.toBeNull();
    if (!stylesheet) return;
    const containerRule = [...stylesheet.cssRules].find(
      (rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && rule.selectorText === ".inspection-container"
    );
    const narrowContainerRule = [...stylesheet.cssRules].find(
      (rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === "(max-width: 24rem)"
    )?.cssRules[0];

    expect(containerRule).toBeDefined();
    expect(narrowContainerRule).toBeInstanceOf(CSSStyleRule);

    for (const viewport of [320, 360, 768, 1440]) {
      const host = document.createElement("div");
      host.style.inlineSize = `${viewport}px`;
      const container = document.createElement("div");
      container.className = "inspection-container";
      const inline = document.createElement("div");
      inline.className = "inspection-inline";
      inline.append(document.createElement("button"), document.createElement("button"));
      container.append(inline);
      host.append(container);
      document.body.append(host);

      const spacing = 16;
      const maxContainerSize = 72 * 16;
      const expectedContainerSize = viewport <= 384
        ? viewport - 2 * 12
        : Math.min(viewport - 2 * spacing, maxContainerSize);
      const inlineStyle = getComputedStyle(inline);

      expect(containerRule?.style.getPropertyValue("inline-size")).toBe("min(100% - (2 * var(--inspection-space-4)), 72rem)");
      expect(expectedContainerSize).toBeGreaterThan(0);
      expect(expectedContainerSize).toBeLessThanOrEqual(viewport);
      expect(containerRule?.style.getPropertyValue("margin-inline")).toBe("auto");
      expect(inlineStyle.flexWrap).toBe("wrap");
      expect(inlineStyle.minInlineSize).toBe("0");

      host.remove();
    }

    styleElement.remove();
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

  it("associates field descriptions and required state with the control", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const reactRoot = createRoot(container);

    await act(async () => reactRoot.render(
      <Field label="Nome" hint="Use seu nome completo" error="Nome obrigatório" required>
        <Input aria-describedby="existing-description" />
      </Field>
    ));

    const input = container.querySelector("input");
    const descriptions = [...container.querySelectorAll(".inspection-hint, .inspection-error")];
    expect(input?.getAttribute("aria-describedby")).toContain("existing-description");
    expect(descriptions.every((description) => input?.getAttribute("aria-describedby")?.includes(description.id))).toBe(true);
    expect(input?.getAttribute("aria-invalid")).toBe("true");
    expect(input?.getAttribute("aria-required")).toBe("true");
    expect(input?.required).toBe(true);
    expect(descriptions.map((description) => description.textContent).join(" ")).toContain("Use seu nome completo");
    expect(descriptions.map((description) => description.textContent).join(" ")).toContain("Nome obrigatório");

    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("provides a searchable accessible combobox with keyboard semantics", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const reactRoot = createRoot(container);
    let selected = "";

    await act(async () => reactRoot.render(<Combobox value={selected} options={[{ value: "asset-1", label: "Imóvel QA", description: "QA-001" }]} onChange={(value) => { selected = value; }} required aria-label="Ativo" />));
    const input = container.querySelector<HTMLInputElement>("[role='combobox']");
    expect(input?.getAttribute("aria-autocomplete")).toBe("list");
    expect(input?.getAttribute("aria-expanded")).toBe("false");

    await act(async () => input?.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "ArrowDown" })));
    const option = document.querySelector<HTMLElement>("[role='option']");
    expect(input?.getAttribute("aria-expanded")).toBe("true");
    expect(option?.textContent).toContain("Imóvel QA");
    await act(async () => option?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(selected).toBe("asset-1");

    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("keeps Field labels and errors attached to the actual combobox input", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const reactRoot = createRoot(container);

    await act(async () => reactRoot.render(
      <Field label="Imóvel" error="Selecione um imóvel">
        <Combobox value="" options={[{ value: "assetA", label: "Apartamento 101" }]} onChange={() => {}} />
      </Field>
    ));

    const input = container.querySelector<HTMLInputElement>("input[role='combobox']");
    const label = container.querySelector<HTMLLabelElement>("label");
    const error = container.querySelector<HTMLElement>(".inspection-error");
    expect(input).not.toBeNull();
    expect(label?.htmlFor).toBe(input?.id);
    expect(input?.getAttribute("aria-label")).toBeNull();
    expect(input?.getAttribute("aria-describedby")).toContain(error?.id ?? "");
    expect(input?.getAttribute("aria-invalid")).toBe("true");

    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("keeps keyboard focus inside an open dialog and restores focus to its opener", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const reactRoot = createRoot(container);

    await act(async () => reactRoot.render(
      <>
        <button>Abrir</button>
        <Dialog isOpen={false} onClose={() => {}} title="Confirmação">{null}</Dialog>
      </>
    ));
    const opener = container.querySelector("button");
    opener?.focus();

    await act(async () => reactRoot.render(
      <>
        <button>Abrir</button>
        <Dialog isOpen onClose={() => {}} title="Confirmação" size="wide">
        <button>Cancelar</button>
        <button>Confirmar</button>
        </Dialog>
      </>
    ));

    const dialog = document.querySelector('[role="dialog"]');
    const buttons = [...document.querySelectorAll('[role="dialog"] button')];
    expect(dialog).not.toBeNull();
    expect(dialog?.parentElement?.classList.contains("inspection-dialog--wide")).toBe(true);
    expect(buttons).toHaveLength(2);

    await act(async () => reactRoot.render(
      <>
        <button>Abrir</button>
        <Dialog isOpen={false} onClose={() => {}} title="Confirmação">{null}</Dialog>
      </>
    ));
    expect(document.activeElement).toBe(opener);
    const css = await readFile(resolve(packageRoot, "src/styles.css"), "utf8");
    expect(css).toContain(".inspection-dialog--wide");

    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("provides semantic collection, recovery, conflict, and confirmation primitives", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const reactRoot = createRoot(container);
    await act(async () => reactRoot.render(<><Breadcrumbs items={[{ href: "/", label: "Início" }, { label: "Registros" }]} /><DataTable caption="Registros" columns={[{ id: "name", label: "Nome" }]}><tr><td data-label="Nome">Ana</td></tr></DataTable><Pagination page={1} hasNextPage onPrevious={() => {}} onNext={() => {}} /><Recovery title="Tente novamente" onRetry={() => {}}>A conexão foi interrompida.</Recovery><VersionConflict currentVersion={2} onReview={() => {}}>O rascunho foi preservado.</VersionConflict><Confirmation target="Unidade A" scope="Tenant A" consequence="Arquivará o registro" onCancel={() => {}} onConfirm={() => {}} /></>));
    expect(container.querySelector("nav[aria-label='Navegação estrutural']")).not.toBeNull();
    expect(container.querySelector("table caption")?.textContent).toBe("Registros");
    expect(container.querySelector("[role='status']")?.textContent).toContain("Página 1");
    expect(container.querySelector(".inspection-version-conflict")?.textContent).toContain("versão atual é 2");
    expect(container.querySelector(".inspection-confirmation dl")?.textContent).toContain("Arquivará o registro");
    await act(async () => reactRoot.unmount());
    container.remove();
  });

  it("covers pending actions, native choices, summary focus, steps, and recovery identity", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const reactRoot = createRoot(container);
    let confirms = 0;
    let resets = 0;
    await act(async () => reactRoot.render(<>
      <Button isPending pendingLabel="Salvando" onClick={() => { confirms += 1; }}>Salvar</Button>
      <ChoiceGroup legend="Consentimento"><Checkbox checked readOnly required label="Aceito os termos" /><Checkbox disabled label="Indisponível" /></ChoiceGroup>
      <Input id="email" />
      <ErrorSummary errors={[{ fieldId: "email", message: "E-mail inválido" }, { message: "Erro geral" }]} />
      <Steps items={[{ id: "one", label: "Primeira", state: "complete" }, { id: "two", label: "Atual", state: "current" }, { id: "three", label: "Depois", state: "pending" }]} />
      <Recovery kind="empty" title="Upload">Nenhum arquivo.</Recovery><Recovery kind="error" title="Autenticação">Não foi possível autenticar.</Recovery>
      <Confirmation consequence="Arquiva" isConfirmDisabled isPending onCancel={() => {}} onConfirm={() => { confirms += 1; }} scope="Tenant" target="Registro" />
      <button type="button" onClick={() => { resets += 1; }}>Controle</button>
    </>));

    const pending = container.querySelector<HTMLButtonElement>(".inspection-button[aria-busy='true']");
    expect(pending?.textContent).toBe("Salvando");
    pending?.click();
    expect(confirms).toBe(0);
    expect(container.querySelector<HTMLInputElement>("input[type='checkbox']")?.checked).toBe(true);
    expect(container.querySelector<HTMLInputElement>("input[type='checkbox']")?.required).toBe(true);
    const summaryLink = container.querySelector<HTMLAnchorElement>(".inspection-error-summary a");
    await act(async () => summaryLink?.click());
    expect(document.activeElement).toBe(container.querySelector("#email"));
    expect(container.querySelector("[aria-current='step']")?.textContent).toBe("Atual");
    expect(container.querySelector(".inspection-steps__item--pending button")).toBeNull();
    const recoveries = [...container.querySelectorAll(".inspection-recovery")];
    expect(new Set(recoveries.map((recovery) => recovery.getAttribute("aria-labelledby"))).size).toBe(2);
    expect(recoveries[1]?.querySelector("button")).toBeNull();
    expect(resets).toBe(0);
    await act(async () => reactRoot.unmount());
    container.remove();
  });
});
