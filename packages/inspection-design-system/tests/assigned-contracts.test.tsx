import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  Breadcrumbs,
  Button,
  Checkbox,
  ChoiceGroup,
  Combobox,
  Confirmation,
  DataTable,
  Dialog,
  ErrorSummary,
  Field,
  FilterBar,
  Icon,
  Input,
  Motion,
  Navigation,
  Pagination,
  Radio,
  Recovery,
  Select,
  Status,
  Steps,
  Textarea,
  VersionConflict,
} from "../src/index.js";

globalThis.IS_REACT_ACT_ENVIRONMENT = true;

async function mount(node: ReactNode) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(node));
  return { container, root };
}

async function unmount(rendered: { container: HTMLElement; root: Root }) {
  await act(async () => rendered.root.unmount());
  rendered.container.remove();
  document.querySelectorAll(".inspection-backdrop").forEach((element) => element.remove());
}

function keydown(element: Element, key: string) {
  element.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key }));
}

describe("assigned shared foundation contracts", () => {
  it("UT-001: Button defaults to a native non-submitting button", async () => {
    let submitted = false;
    const rendered = await mount(<form onSubmit={(event) => { event.preventDefault(); submitted = true; }}><Button>Salvar</Button></form>);
    const button = rendered.container.querySelector<HTMLButtonElement>("button");
    expect(button?.type).toBe("button");
    button?.click();
    expect(submitted).toBe(false);
    await unmount(rendered);
  });

  it("UT-002: pending Button shows its pending label and rejects activation", async () => {
    let activations = 0;
    const rendered = await mount(<Button isPending pendingLabel="Salvando" onClick={() => { activations += 1; }}>Salvar</Button>);
    const button = rendered.container.querySelector<HTMLButtonElement>("button");
    expect(button?.textContent).toBe("Salvando");
    expect(button?.disabled).toBe(true);
    button?.click();
    expect(activations).toBe(0);
    await unmount(rendered);
  });

  it("UT-003: a future status can be presented as readable neutral text", async () => {
    const rendered = await mount(<Status tone="info">Situação não reconhecida</Status>);
    expect(rendered.container.textContent).toContain("Situação não reconhecida");
    expect(rendered.container.querySelector(".inspection-status")?.className).not.toContain("success");
    await unmount(rendered);
  });

  it("UT-004: danger Status has text and a non-color icon", async () => {
    const rendered = await mount(<Status tone="danger">Rejeitada</Status>);
    expect(rendered.container.textContent).toContain("Rejeitada");
    expect(rendered.container.querySelector("svg")).not.toBeNull();
    expect(rendered.container.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    await unmount(rendered);
  });

  it("UT-005: reduced-motion CSS suppresses nonessential motion", async () => {
    const rendered = await mount(<Motion>Conteúdo</Motion>);
    const css = await readFile(resolve(import.meta.dirname, "../src/styles.css"), "utf8");
    expect(rendered.container.querySelector(".inspection-motion")).not.toBeNull();
    expect(css).toContain("@media (prefers-reduced-motion: reduce)");
    expect(css).toContain("transition-duration: 0.01ms !important");
    await unmount(rendered);
  });

  it("UT-006: layout primitives allow long content to wrap", async () => {
    const rendered = await mount(<div className="inspection-container"><div className="inspection-stack"><div className="inspection-inline"><span>Texto longo</span></div></div></div>);
    expect(rendered.container.querySelector(".inspection-inline")?.className).toContain("inspection-inline");
    const css = await readFile(resolve(import.meta.dirname, "../src/styles.css"), "utf8");
    expect(css).toContain(".inspection-inline { display: flex; flex-wrap: wrap;");
    expect(css).toContain("min-inline-size: 0");
    await unmount(rendered);
  });

  it("UT-007: decorative Icon does not add a second accessible label", async () => {
    const rendered = await mount(<Button><Icon name="check" />Salvar</Button>);
    expect(rendered.container.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    expect(rendered.container.querySelector("button")?.textContent).toContain("Salvar");
    await unmount(rendered);
  });

  it("UT-008: Field preserves an explicit input id and label association", async () => {
    const rendered = await mount(<Field label="E-mail"><Input id="email" /></Field>);
    const input = rendered.container.querySelector("input");
    expect(input?.id).toBe("email");
    expect(rendered.container.querySelector("label")?.getAttribute("for")).toBe("email");
    await unmount(rendered);
  });

  it("UT-009: Field errors mark the actual input and describe it", async () => {
    const rendered = await mount(<Field label="E-mail" error="Informe um e-mail válido"><Input /></Field>);
    const input = rendered.container.querySelector("input");
    const error = rendered.container.querySelector(".inspection-error");
    expect(input?.getAttribute("aria-invalid")).toBe("true");
    expect(input?.getAttribute("aria-describedby")).toContain(error?.id ?? "");
    await unmount(rendered);
  });

  it("UT-010: Field retains existing and generated descriptions exactly once", async () => {
    const rendered = await mount(<Field label="Nome" hint="Use seu nome" error="Nome obrigatório"><Input aria-describedby="policy" /></Field>);
    const input = rendered.container.querySelector("input");
    const describedBy = input?.getAttribute("aria-describedby") ?? "";
    expect(describedBy.split(" ").filter(Boolean)).toEqual(expect.arrayContaining(["policy"]));
    expect(new Set(describedBy.split(" ").filter(Boolean)).size).toBe(describedBy.split(" ").filter(Boolean).length);
    expect(rendered.container.querySelector(".inspection-hint")?.id).toBeTruthy();
    expect(rendered.container.querySelector(".inspection-error")?.id).toBeTruthy();
    await unmount(rendered);
  });

  it("UT-011: native Select and Textarea forward labels, required, and refs", async () => {
    const selectRef = { current: null as HTMLSelectElement | null };
    const textareaRef = { current: null as HTMLTextAreaElement | null };
    const rendered = await mount(<><Field label="Tipo" required><Select ref={selectRef}><option value="a">A</option></Select></Field><Field label="Descrição" required><Textarea ref={textareaRef} /></Field></>);
    expect(selectRef.current).toBe(rendered.container.querySelector("select"));
    expect(textareaRef.current).toBe(rendered.container.querySelector("textarea"));
    expect(rendered.container.querySelector("select")?.required).toBe(true);
    expect(rendered.container.querySelector("textarea")?.required).toBe(true);
    await unmount(rendered);
  });

  it("UT-012: ErrorSummary focuses an existing field", async () => {
    const rendered = await mount(<><Input id="email" /><ErrorSummary errors={[{ fieldId: "email", message: "E-mail inválido" }]} /></>);
    rendered.container.querySelector<HTMLAnchorElement>(".inspection-error-summary a")?.click();
    expect(document.activeElement).toBe(rendered.container.querySelector("#email"));
    await unmount(rendered);
  });

  it("UT-013: form-level ErrorSummary messages do not create broken links", async () => {
    const rendered = await mount(<ErrorSummary errors={[{ message: "Não foi possível salvar" }]} />);
    expect(rendered.container.querySelector("a")).toBeNull();
    expect(rendered.container.textContent).toContain("Não foi possível salvar");
    await unmount(rendered);
  });

  it("UT-014: required checkbox choices use fieldset and legend semantics", async () => {
    const rendered = await mount(<ChoiceGroup legend="Consentimento"><Checkbox required label="Aceito" /></ChoiceGroup>);
    expect(rendered.container.querySelector("fieldset legend")?.textContent).toBe("Consentimento");
    expect(rendered.container.querySelector<HTMLInputElement>("input[type='checkbox']")?.required).toBe(true);
    await unmount(rendered);
  });

  it("UT-015: disabled radio choices preserve the checked sibling", async () => {
    const rendered = await mount(<ChoiceGroup legend="Escolha"><Radio name="choice" value="a" checked readOnly label="A" /><Radio name="choice" value="b" disabled label="B" /></ChoiceGroup>);
    const disabled = rendered.container.querySelector<HTMLInputElement>("input[value='b']");
    disabled?.click();
    expect(rendered.container.querySelector<HTMLInputElement>("input[value='a']")?.checked).toBe(true);
    expect(disabled?.checked).toBe(false);
    await unmount(rendered);
  });

  it("UT-016: Combobox displays a selected label while returning its identifier", async () => {
    let selected = "assetA";
    const rendered = await mount(<Combobox aria-label="Imóvel" value={selected} options={[{ value: "assetA", label: "Apartamento 101" }]} onChange={(value) => { selected = value; }} />);
    const input = rendered.container.querySelector<HTMLInputElement>("input[role='combobox']");
    expect(input?.value).toBe("Apartamento 101");
    await act(async () => input && keydown(input, "ArrowDown"));
    await act(async () => input && keydown(input, "Enter"));
    expect(selected).toBe("assetA");
    await unmount(rendered);
  });

  it("UT-017: unmatched required Combobox text does not become an identifier", async () => {
    let selected = "";
    const rendered = await mount(<Combobox aria-label="Imóvel" required value="" options={[{ value: "assetA", label: "Apartamento 101" }]} onChange={(value) => { selected = value; }} />);
    const input = rendered.container.querySelector<HTMLInputElement>("input[role='combobox']")!;
    await act(async () => { input.value = "imóvel inexistente"; input.dispatchEvent(new Event("input", { bubbles: true })); });
    expect(selected).toBe("");
    expect(input.required).toBe(true);
    await unmount(rendered);
  });

  it("UT-018: empty Combobox collections announce no results without a false active descendant", async () => {
    const rendered = await mount(<Combobox aria-label="Imóvel" value="" options={[]} onChange={() => {}} />);
    const input = rendered.container.querySelector<HTMLInputElement>("input[role='combobox']")!;
    await act(async () => keydown(input, "ArrowDown"));
    expect(document.querySelector(".inspection-combobox-empty")?.textContent).toContain("Nenhuma opção");
    expect(input.getAttribute("aria-activedescendant")).toBeNull();
    await unmount(rendered);
  });

  it("UT-019: keyboard selection invokes onChange once with the option identifier", async () => {
    const selected: string[] = [];
    const rendered = await mount(<Combobox aria-label="Imóvel" value="" options={[{ value: "assetA", label: "Apartamento 101" }, { value: "assetB", label: "Apartamento 202" }]} onChange={(value) => selected.push(value)} />);
    const input = rendered.container.querySelector<HTMLInputElement>("input[role='combobox']")!;
    await act(async () => keydown(input, "ArrowDown"));
    await act(async () => keydown(input, "Enter"));
    expect(selected).toEqual(["assetA"]);
    await unmount(rendered);
  });

  it("UT-020: Escape closes a Combobox popup before its containing Dialog", async () => {
    let closed = 0;
    const rendered = await mount(<Dialog isOpen title="Nova vistoria" onClose={() => { closed += 1; }}><Combobox aria-label="Imóvel" value="" options={[{ value: "assetA", label: "Apartamento 101" }]} onChange={() => {}} /></Dialog>);
    const input = document.querySelector<HTMLInputElement>("input[role='combobox']")!;
    await act(async () => keydown(input, "ArrowDown"));
    await act(async () => keydown(input, "Escape"));
    expect(document.querySelector("[role='dialog']")).not.toBeNull();
    expect(closed).toBe(0);
    await unmount(rendered);
  });

  it("UT-021: Combobox accessibility props and ref reach the actual input", async () => {
    const inputRef = { current: null as HTMLInputElement | null };
    const rendered = await mount(<Combobox id="asset" aria-label="Imóvel" aria-describedby="asset-help" aria-invalid value="" options={[]} onChange={() => {}} inputRef={inputRef} />);
    const input = rendered.container.querySelector<HTMLInputElement>("input[role='combobox']");
    expect(inputRef.current).toBe(input);
    expect(input?.id).toBe("asset");
    expect(input?.getAttribute("aria-describedby")).toBe("asset-help");
    expect(input?.getAttribute("aria-invalid")).toBe("true");
    await unmount(rendered);
  });

  it("UT-022: disabled Combobox ignores selection attempts", async () => {
    let selected = "";
    const rendered = await mount(<Combobox aria-label="Imóvel" disabled value="" options={[{ value: "assetA", label: "Apartamento 101" }]} onChange={(value) => { selected = value; }} />);
    const input = rendered.container.querySelector<HTMLInputElement>("input[role='combobox']")!;
    await act(async () => keydown(input, "ArrowDown"));
    await act(async () => keydown(input, "Enter"));
    expect(selected).toBe("");
    expect(input.disabled).toBe(true);
    await unmount(rendered);
  });

  it("UT-023: a stale Combobox value is not mapped to another option", async () => {
    const rendered = await mount(<Combobox aria-label="Imóvel" value="assetA" options={[{ value: "assetB", label: "Apartamento 202" }]} onChange={() => {}} />);
    const input = rendered.container.querySelector<HTMLInputElement>("input[role='combobox']")!;
    expect(input.value).not.toBe("Apartamento 202");
    await unmount(rendered);
  });

  it("UT-024: Dialog has its accessible name and focuses inside", async () => {
    const rendered = await mount(<Dialog isOpen title="Nova vistoria" onClose={() => {}}><Button>Confirmar</Button></Dialog>);
    expect(document.querySelector('[role="dialog"]')?.getAttribute("aria-label")).toBe("Nova vistoria");
    expect(document.activeElement?.closest('[role="dialog"]')).not.toBeNull();
    await unmount(rendered);
  });

  it("UT-025: Dialog focus remains within its enabled controls", async () => {
    const rendered = await mount(<Dialog isOpen title="Nova vistoria" onClose={() => {}}><Button>Primeiro</Button><Button>Último</Button></Dialog>);
    const controls = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')];
    controls[controls.length - 1]?.focus();
    await act(async () => keydown(controls[controls.length - 1], "Tab"));
    expect(document.activeElement?.closest('[role="dialog"]')).not.toBeNull();
    await act(async () => keydown(controls[0], "Tab"));
    expect(document.activeElement?.closest('[role="dialog"]')).not.toBeNull();
    await unmount(rendered);
  });

  it("UT-026: non-dismissable Dialog ignores Escape and backdrop dismissal", async () => {
    let closed = 0;
    const rendered = await mount(<Dialog isOpen isDismissable={false} title="Pendente" onClose={() => { closed += 1; }}><p>Processando</p></Dialog>);
    await act(async () => keydown(document.querySelector('[role="dialog"]')!, "Escape"));
    document.querySelector<HTMLElement>(".inspection-backdrop")?.click();
    expect(closed).toBe(0);
    await unmount(rendered);
  });

  it("UT-027: Dialog restores focus to a mounted opener", async () => {
    const opener = document.createElement("button");
    opener.textContent = "Abrir";
    document.body.append(opener);
    opener.focus();
    const rendered = await mount(<Dialog isOpen title="Detalhe" onClose={() => {}} />);
    await act(async () => rendered.root.render(<Dialog isOpen={false} title="Detalhe" onClose={() => {}} />));
    expect(document.activeElement).toBe(opener);
    opener.remove();
    await unmount(rendered);
  });

  it("UT-028: Dialog uses a surviving fallback focus target", async () => {
    const fallback = document.createElement("button");
    fallback.textContent = "Vistorias";
    document.body.append(fallback);
    const trigger = document.createElement("button");
    document.body.append(trigger);
    trigger.focus();
    const rendered = await mount(<Dialog isOpen title="Nova vistoria" restoreFocusRef={{ current: fallback }} onClose={() => {}} />);
    trigger.remove();
    await act(async () => rendered.root.render(<Dialog isOpen={false} title="Nova vistoria" restoreFocusRef={{ current: fallback }} onClose={() => {}} />));
    expect(document.activeElement).toBe(fallback);
    fallback.remove();
    await unmount(rendered);
  });

  it("UT-029: disabled Confirmation cannot confirm an empty reason", async () => {
    let confirmed = 0;
    const rendered = await mount(<Confirmation target="Vistoria" scope="Imobiliária" consequence="Invalidar" isConfirmDisabled onCancel={() => {}} onConfirm={() => { confirmed += 1; }} />);
    rendered.container.querySelector<HTMLButtonElement>(".inspection-button--primary")?.click();
    expect(confirmed).toBe(0);
    await unmount(rendered);
  });

  it("UT-030: pending Confirmation rejects repeated confirm activation", async () => {
    let confirmed = 0;
    const rendered = await mount(<Confirmation target="Vistoria" scope="Imobiliária" consequence="Invalidar" isPending onCancel={() => {}} onConfirm={() => { confirmed += 1; }} />);
    rendered.container.querySelector<HTMLButtonElement>(".inspection-button--primary")?.click();
    expect(confirmed).toBe(0);
    expect(rendered.container.querySelector<HTMLButtonElement>(".inspection-button--secondary")?.disabled).toBe(true);
    await unmount(rendered);
  });

  it("UT-031: canceling Confirmation does not confirm", async () => {
    let canceled = 0;
    let confirmed = 0;
    const rendered = await mount(<Confirmation target="Vistoria" scope="Imobiliária" consequence="Invalidar" onCancel={() => { canceled += 1; }} onConfirm={() => { confirmed += 1; }} />);
    rendered.container.querySelector<HTMLButtonElement>(".inspection-button--secondary")?.click();
    expect(canceled).toBe(1);
    expect(confirmed).toBe(0);
    await unmount(rendered);
  });

  it("UT-032: DataTable renders caption and scoped column headers", async () => {
    const rendered = await mount(<DataTable caption="Vistorias" columns={[{ id: "status", label: "Situação" }]}><tr><td>Concluída</td></tr></DataTable>);
    expect(rendered.container.querySelector("caption")?.textContent).toBe("Vistorias");
    expect(rendered.container.querySelector("th")?.getAttribute("scope")).toBe("col");
    await unmount(rendered);
  });

  it("UT-033: unavailable pagination directions are disabled without callbacks", async () => {
    let calls = 0;
    const rendered = await mount(<Pagination page={1} hasNextPage={false} onPrevious={() => { calls += 1; }} onNext={() => { calls += 1; }} />);
    const buttons = [...rendered.container.querySelectorAll<HTMLButtonElement>("button")];
    buttons.forEach((button) => button.click());
    expect(buttons.every((button) => button.disabled)).toBe(true);
    expect(calls).toBe(0);
    await unmount(rendered);
  });

  it("UT-034: controlled pagination exposes callbacks without network behavior", async () => {
    let previous = 0;
    let next = 0;
    const rendered = await mount(<Pagination page={2} hasNextPage onPrevious={() => { previous += 1; }} onNext={() => { next += 1; }} />);
    const buttons = [...rendered.container.querySelectorAll<HTMLButtonElement>("button")];
    buttons[0].click();
    buttons[1].click();
    expect(previous).toBe(1);
    expect(next).toBe(1);
    expect(rendered.container.textContent).toContain("Página 2");
    await unmount(rendered);
  });

  it("UT-035: Navigation marks only the current item", async () => {
    const rendered = await mount(<Navigation label="Principal" items={[{ href: "/", label: "Início", current: true }, { href: "/vistorias", label: "Vistorias" }]} />);
    expect(rendered.container.querySelectorAll('[aria-current="page"]')).toHaveLength(1);
    await unmount(rendered);
  });

  it("UT-036: final Breadcrumb is current and is not a self-link", async () => {
    const rendered = await mount(<Breadcrumbs items={[{ href: "/", label: "Início" }, { href: "/vistorias", label: "Vistorias" }]} />);
    const items = rendered.container.querySelectorAll("li");
    expect(items[1]?.querySelector("a")).toBeNull();
    expect(items[1]?.querySelector('[aria-current="page"]')).not.toBeNull();
    await unmount(rendered);
  });

  it("UT-037: pending non-selectable Steps are not bypass controls", async () => {
    const rendered = await mount(<Steps items={[{ id: "current", label: "Atual", state: "current" }, { id: "later", label: "Depois", state: "pending" }]} />);
    expect(rendered.container.querySelector(".inspection-steps__item--pending button")).toBeNull();
    await unmount(rendered);
  });

  it("UT-038: Steps preserve source order and name the current step", async () => {
    const rendered = await mount(<Steps items={[{ id: "one", label: "Um", state: "complete" }, { id: "two", label: "Dois", state: "current" }, { id: "three", label: "Três", state: "pending" }]} />);
    expect([...rendered.container.querySelectorAll("li")].map((item) => item.textContent)).toEqual(["Um", "Dois", "Três"]);
    expect(rendered.container.querySelector('[aria-current="step"]')?.textContent).toBe("Dois");
    await unmount(rendered);
  });

  it("UT-039: FilterBar reset does not submit its containing form", async () => {
    let submitted = 0;
    let reset = 0;
    const rendered = await mount(<FilterBar onReset={() => { reset += 1; }}><Input name="search" /></FilterBar>);
    const form = rendered.container.querySelector("form")!;
    form.addEventListener("submit", () => { submitted += 1; });
    rendered.container.querySelector<HTMLButtonElement>("button")?.click();
    expect(reset).toBe(1);
    expect(submitted).toBe(0);
    await unmount(rendered);
  });

  it("UT-040: Recovery instances use distinct heading references", async () => {
    const rendered = await mount(<><Recovery title="Upload">Falhou</Recovery><Recovery title="Autenticação">Falhou</Recovery></>);
    const recoveries = [...rendered.container.querySelectorAll<HTMLElement>(".inspection-recovery")];
    expect(new Set(recoveries.map((item) => item.getAttribute("aria-labelledby"))).size).toBe(2);
    expect(recoveries[0].querySelector("h2")?.id).toBe(recoveries[0].getAttribute("aria-labelledby"));
    await unmount(rendered);
  });

  it("UT-041: error Recovery without onRetry has no inert retry button", async () => {
    const rendered = await mount(<Recovery kind="error" title="Falhou">Erro</Recovery>);
    expect(rendered.container.querySelector("button")).toBeNull();
    await unmount(rendered);
  });

  it("UT-042: empty and error Recovery variants remain distinguishable", async () => {
    const rendered = await mount(<><Recovery kind="empty" title="Coleção">Nenhum resultado</Recovery><Recovery kind="error" title="Coleção">Falha ao carregar</Recovery></>);
    const recoveries = [...rendered.container.querySelectorAll<HTMLElement>(".inspection-recovery")];
    expect(recoveries[0].className).toContain("--empty");
    expect(recoveries[1].className).toContain("--error");
    expect(recoveries[0].textContent).not.toBe(recoveries[1].textContent);
    await unmount(rendered);
  });

  it("UT-043: Status rows do not create live announcements", async () => {
    const rendered = await mount(<Status>Atualizada</Status>);
    expect(rendered.container.querySelector("[aria-live]")).toBeNull();
    await unmount(rendered);
  });

  it("UT-044: VersionConflict offers only current-state review", async () => {
    let reviewed = 0;
    const rendered = await mount(<VersionConflict currentVersion={4} onReview={() => { reviewed += 1; }}>Revise</VersionConflict>);
    const buttons = [...rendered.container.querySelectorAll("button")];
    expect(buttons).toHaveLength(1);
    buttons[0].click();
    expect(reviewed).toBe(1);
    await unmount(rendered);
  });
});
