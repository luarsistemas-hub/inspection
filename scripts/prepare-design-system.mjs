import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, utimesSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, relative, resolve } from "node:path";
import { execFileSync } from "node:child_process";

const workspace = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const packageDir = resolve(workspace, "packages/inspection-design-system");
const lockPath = resolve(packageDir, ".build-lock");
const fingerprintPath = resolve(packageDir, "dist/.source-fingerprint");
mkdirSync(packageDir, { recursive: true });

let acquired = false;
let heartbeat;
while (!acquired) {
  try { mkdirSync(lockPath); acquired = true; }
  catch (error) {
    if (error?.code !== "EEXIST") throw error;
    let lockAge;
    try { lockAge = Date.now() - statSync(lockPath).mtimeMs; }
    catch (statError) { if (statError?.code === "ENOENT") continue; throw statError; }
    if (lockAge > 120_000) { rmSync(lockPath, { recursive: true, force: true }); continue; }
    await new Promise((resolvePromise) => setTimeout(resolvePromise, 100));
  }
}

try {
  writeFileSync(resolve(lockPath, "owner"), `${process.pid}\n${Date.now()}`);
  heartbeat = setInterval(() => { try { utimesSync(lockPath, new Date(), new Date()); } catch { /* a stale-lock contender may have replaced it */ } }, 15_000);
  heartbeat.unref();
  if (!existsSync(resolve(packageDir, "node_modules"))) execFileSync("npm", ["ci"], { cwd: packageDir, stdio: "inherit" });
  const watchedFiles = ["package.json", "package-lock.json", "tsconfig.json", "tsconfig.build.json", "scripts/copy-css.mjs"];
  const collect = (path) => readdirSync(path, { withFileTypes: true }).flatMap((entry) => {
    const file = resolve(path, entry.name);
    if (entry.isDirectory()) return collect(file);
    return /\.(?:ts|tsx|css|woff2|txt)$/.test(entry.name) ? [file] : [];
  });
  const sourceFiles = [...watchedFiles.map((name) => resolve(packageDir, name)), ...collect(resolve(packageDir, "src"))].sort();
  const fingerprint = createHash("sha256");
  for (const file of sourceFiles) { fingerprint.update(relative(packageDir, file)); fingerprint.update(readFileSync(file)); }
  const digest = fingerprint.digest("hex");
  const hasOutput = existsSync(resolve(packageDir, "dist/index.js")) && existsSync(resolve(packageDir, "dist/styles.css")) && existsSync(fingerprintPath);
  if (!hasOutput || readFileSync(fingerprintPath, "utf8") !== digest) {
    execFileSync("npm", ["run", "build"], { cwd: packageDir, stdio: "inherit" });
    writeFileSync(fingerprintPath, digest);
  }
} finally { clearInterval(heartbeat); rmSync(lockPath, { recursive: true, force: true }); }
