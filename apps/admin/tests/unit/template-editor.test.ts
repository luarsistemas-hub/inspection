import * as React from "react";
import { act, createElement, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import { TemplateEditorPage } from "@/features/admin/admin-shell";

vi.mock("@/graphql/client", () => ({ graphql: vi.fn() }));
vi.mock("@inspection/address", () => ({ AddressFieldStyles: () => null, AddressForm: () => null, emptyPostalAddress: {} }));
vi.mock("@inspection/design-system", async () => {
  const React = await import("react");
  return {
    Button: ({ children, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) => React.createElement("button", props, children),
    Field: ({ label, children, ...props }: { label: string; children: ReactNode }) => React.createElement("label", props, label, children),
    Input: (props: InputHTMLAttributes<HTMLInputElement>) => React.createElement("input", props),
    Select: (props: SelectHTMLAttributes<HTMLSelectElement>) => React.createElement("select", props),
    Textarea: (props: TextareaHTMLAttributes<HTMLTextAreaElement>) => React.createElement("textarea", props),
    Checkbox: ({ label, ...props }: InputHTMLAttributes<HTMLInputElement> & { label: string }) => React.createElement("label", null, React.createElement("input", { ...props, type: "checkbox" }), label),
    Alert: ({ children }: { children: ReactNode }) => React.createElement("div", { role: "alert" }, children),
  };
});

const operationName = (document: unknown) => (document as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
const catalogs = (templates: unknown[] = []) => ({ templates: { nodes: templates, pageInfo: { endCursor: null, hasNextPage: false } }, segmentDefinitions: { nodes: [{ id: "segment-a", activeVersionId: "segment-version-a", name: "Residencial", key: "residential" }] } });

describe("TemplateEditorPage", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, React });

  it("UT-362 creates a v2 environment-capture definition from the real editor", async () => {
    const calls: Array<{ operation: string; variables: unknown }> = [];
    vi.mocked(graphql).mockImplementation(async (document, variables) => {
      const operation = operationName(document) ?? ""; calls.push({ operation, variables });
      if (operation === "AdminCatalogs") return catalogs() as never;
      if (operation === "AdminPublishTemplateVersion") return { publishTemplateVersion: { template: { id: "template-a", version: 1 }, version: { id: "v2", versionNumber: 2 }, userErrors: [] } } as never;
      if (operation === "AdminActivateTemplateVersion") return { activateTemplateVersion: { template: { id: "template-a" }, version: { versionNumber: 2 }, userErrors: [] } } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(TemplateEditorPage)); await new Promise((resolve) => setTimeout(resolve, 0)); });
    await act(async () => { [...host.querySelectorAll("button")].find((button) => button.textContent === "Novo modelo")?.click(); });
    expect([...host.querySelectorAll("legend")].map((legend) => legend.textContent)).toContain("Captura por ambiente · schema v2");
    const fields = [...host.querySelectorAll("input")];
    await act(async () => { fields[0].value = "qa-template"; fields[0].dispatchEvent(new Event("input", { bubbles: true })); fields[1].value = "Modelo QA"; fields[1].dispatchEvent(new Event("input", { bubbles: true })); });
    const segment = host.querySelector<HTMLSelectElement>("select:not([aria-label])");
    await act(async () => { if (segment) { segment.value = "segment-version-a"; segment.dispatchEvent(new Event("change", { bubbles: true })); } });
    await act(async () => { host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    const published = calls.find((call) => call.operation === "AdminPublishTemplateVersion")?.variables as { input: { definition: Record<string, unknown> } };
    expect(published.input.definition).toMatchObject({ schemaVersion: 2, captureLayout: "ENVIRONMENTS", environmentCapture: { photo: { minimum: 1, maximum: 20 }, video: { maximumClips: 3 } } });
    await act(async () => { root.unmount(); });
  });

  it("UT-362 keeps an existing v1 checklist read-only", async () => {
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = operationName(document);
      if (operation === "AdminCatalogs") return catalogs([{ id: "template-v1", key: "legacy", name: "Modelo legado", activeVersionId: "version-v1", version: 1 }]) as never;
      if (operation === "AdminTemplateVersion") return { templateVersion: { id: "version-v1", schemaVersion: 1, versionNumber: 1, definition: { schemaVersion: 1, segmentVersionId: "segment-version-a", participantRoles: ["TENANT_PARTICIPANT"], defaultComparisonMode: "CHECKLIST_ONLY", requirements: [{ key: "overview", section: "Geral", label: "Visão geral", instructions: "Registrar o ambiente", minimumCount: 1, maximumCount: 3 }], policy: {} } } } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(TemplateEditorPage)); await new Promise((resolve) => setTimeout(resolve, 0)); });
    await act(async () => { [...host.querySelectorAll("button")].find((button) => button.textContent === "Abrir modelo")?.click(); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(host.textContent).toContain("Checklist v1");
    expect(host.textContent).toContain("Sem inventário · schema v1 somente leitura");
    expect(host.textContent).not.toContain("Editar v2");
    expect(host.querySelector("form")).toBeNull();
    await act(async () => { root.unmount(); });
  });
});
