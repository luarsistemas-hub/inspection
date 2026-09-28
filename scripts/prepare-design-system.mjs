import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { execFileSync } from "node:child_process";

const workspace = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const packageDir = resolve(workspace, "packages/inspection-design-system");

if (!existsSync(resolve(packageDir, "node_modules"))) {
  execFileSync("npm", ["ci"], { cwd: packageDir, stdio: "inherit" });
}

if (!existsSync(resolve(packageDir, "dist/index.js")) || !existsSync(resolve(packageDir, "dist/styles.css"))) {
  execFileSync("npm", ["run", "build"], { cwd: packageDir, stdio: "inherit" });
}
