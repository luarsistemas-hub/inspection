import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const packageRoot = resolve(import.meta.dirname, "..");

describe("C11 package boundary", () => {
  it("UT-075: publishes only compiled root and stylesheet entries", async () => {
    const manifest = JSON.parse(await readFile(resolve(packageRoot, "package.json"), "utf8")) as { exports: Record<string, unknown> };
    expect(manifest.exports).toEqual({ ".": { types: "./dist/index.d.ts", import: "./dist/index.js" }, "./styles.css": "./dist/styles.css" });
  });

  it("UT-076: declares the clean-build prerequisite checker", async () => {
    const checker = await readFile(resolve(packageRoot, "../../scripts/prepare-design-system.mjs"), "utf8");
    expect(checker).toContain("dist/index.js");
    expect(checker).toContain("npm\", [\"run\", \"build\"]");
  });

  it("UT-077: keeps React Aria in runtime dependencies", async () => {
    const manifest = JSON.parse(await readFile(resolve(packageRoot, "package.json"), "utf8")) as { dependencies?: Record<string, string>; devDependencies?: Record<string, string> };
    expect(manifest.dependencies?.["react-aria-components"]).toBe("1.21.1");
    expect(manifest.devDependencies?.["react-aria-components"]).toBeUndefined();
  });

  it("UT-078: limits packed contents to compiled output and package metadata", async () => {
    const manifest = JSON.parse(await readFile(resolve(packageRoot, "package.json"), "utf8")) as { files?: string[] };
    expect(manifest.files).toEqual(["dist"]);
  });
});
