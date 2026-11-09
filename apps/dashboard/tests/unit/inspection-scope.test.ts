import { describe, expect, it, vi } from "vitest";
import { confirmScopeAndRefresh, isScopeVersionConflict, scopeSelectionInput, scopeSelectionKey } from "../../src/features/dashboard/inspection-scope";
import { ScopeConflictNotice } from "../../src/features/dashboard/scope-conflict-notice";
import * as React from "react";
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";

describe("confirmação do escopo da vistoria", () => {
  it("UT-094: conflito na versão 1 recarrega o escopo de versão 2", async () => {
    expect(isScopeVersionConflict("CONFLICT")).toBe(true);
    expect(scopeSelectionInput([scopeSelectionKey("kitchen", "floor")])).toEqual([{ roomKey: "kitchen", componentKey: "floor" }]);
    expect(isScopeVersionConflict("INVALID_STATE")).toBe(false);
    const reload = vi.fn(async () => undefined);
    const result = await confirmScopeAndRefresh(1, async (expectedVersion) => {
      expect(expectedVersion).toBe(1);
      return { error: "O escopo foi alterado.", code: "CONFLICT", value: { version: 2 } };
    }, reload);
    expect(result.conflict).toBe(true);
    expect(result.error).toContain("alterado");
    expect(reload).toHaveBeenCalledOnce();
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(ScopeConflictNotice, { message: "O escopo mudou enquanto você revisava.", onReload: reload })); });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Recarregar escopo");
    await act(async () => { host.querySelector("button")?.click(); });
    expect(reload).toHaveBeenCalledTimes(2);
    await act(async () => { root.unmount(); });
  });
});
