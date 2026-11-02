import * as React from "react";
import { act, createElement, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import { VoicePolicyPage } from "@/features/admin/voice-policy-page";

vi.mock("@/graphql/client", async (importOriginal) => ({ ...await importOriginal<typeof import("@/graphql/client")>(), graphql: vi.fn(), graphqlOptionalPermission: vi.fn() }));
vi.mock("@inspection/design-system", async () => {
  const React = await import("react");
  return {
    Alert: ({ children }: { children: ReactNode }) => React.createElement("div", { role: "alert" }, children),
    Button: ({ children, isPending, pendingLabel, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { isPending?: boolean; pendingLabel?: string }) => React.createElement("button", { ...props, "data-pending": isPending, "data-pending-label": pendingLabel }, children),
    Checkbox: ({ label, ...props }: InputHTMLAttributes<HTMLInputElement> & { label: string }) => React.createElement("label", null, label, React.createElement("input", { type: "checkbox", ...props })),
    Field: ({ label, children }: { label: string; children: ReactNode }) => React.createElement("label", null, label, children),
    Input: (props: InputHTMLAttributes<HTMLInputElement>) => React.createElement("input", props),
    Recovery: ({ title, children }: { title: string; children: ReactNode }) => React.createElement("div", null, title, children),
  };
});

describe("VoicePolicyPage", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

  it("requires four ceilings, displays effective limits, and saves the enabled policy", async () => {
    vi.stubGlobal("React", React);
    vi.stubGlobal("crypto", { randomUUID: () => "mutation-1" });
    vi.mocked(graphql).mockImplementation(async (document, variables) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "AdminVoicePolicy") return { voicePolicy: { enabled: false, tenantNoteLimit: null, tenantAudioLimitMs: null, inspectionNoteLimit: null, inspectionAudioLimitMs: null, version: 3 } } as never;
      if (operation === "AdminConfigureVoicePolicy") {
        expect(variables).toMatchObject({ input: { enabled: true, expectedVersion: 3, tenantNoteLimit: 100, tenantAudioLimitMs: 300 * 60_000, inspectionNoteLimit: 20, inspectionAudioLimitMs: 30 * 60_000, clientMutationId: "voice-policy-mutation-1" } });
        return { configureVoicePolicy: { policy: { enabled: true, tenantNoteLimit: 100, tenantAudioLimitMs: 300 * 60_000, inspectionNoteLimit: 20, inspectionAudioLimitMs: 30 * 60_000, version: 4 }, userErrors: [] } } as never;
      }
      throw new Error(`Unexpected operation ${operation}`);
    });

    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(VoicePolicyPage)); await new Promise((resolve) => setTimeout(resolve, 0)); });
    const saveButton = () => [...host.querySelectorAll("button")].find((button) => button.textContent === "Salvar política de voz")!;
    expect(saveButton().disabled).toBe(true);
    expect(host.textContent).toContain("limite de anotações da imobiliária");

    const fields = [...host.querySelectorAll<HTMLInputElement>("input[type=number]")];
    const setValue = async (field: HTMLInputElement, value: string) => {
      await act(async () => {
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;
        setter?.call(field, value);
        field.dispatchEvent(new Event("input", { bubbles: true }));
      });
    };
    await setValue(fields[0], "100");
    await setValue(fields[1], "300");
    await setValue(fields[2], "20");
    await setValue(fields[3], "30");
    expect(saveButton().disabled).toBe(false);
    const enable = host.querySelector<HTMLInputElement>("input[type=checkbox]")!;
    await act(async () => { enable.click(); });
    await act(async () => { host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(host.textContent).toContain("Anotações por voz habilitadas");
    expect(host.textContent).toContain("100 anotações · 300 min de áudio");
    await act(async () => { root.unmount(); });
  });
});
