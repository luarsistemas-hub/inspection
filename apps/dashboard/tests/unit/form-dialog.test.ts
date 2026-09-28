import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const sourcePath = resolve(import.meta.dirname, "../../src/features/dashboard/form-dialog.tsx");

describe("FormDialog", () => {
  it("UT-057 replaces native discard confirmation with an explicit accessible decision", async () => {
    const source = await readFile(sourcePath, "utf8");

    expect(source).toContain("setConfirmDiscard(true)");
    expect(source).toContain('title="Descartar alterações?"');
    expect(source).toContain("onCancel={() => setConfirmDiscard(false)}");
    expect(source).not.toContain("window.confirm");
  });

  it("UT-058 prevents closing the modal while a committed form request is pending", async () => {
    const source = await readFile(sourcePath, "utf8");

    expect(source).toContain("isDismissable={!busy && !confirmDiscard}");
    expect(source).toContain("if (busy) return;");
    expect(source).toContain("disabled={busy}");
  });
});
