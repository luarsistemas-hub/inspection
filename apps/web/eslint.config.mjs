import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default [
  { ignores: [".next/**", "node_modules/**", "playwright-report/**", "public/sw.js", "next-env.d.ts"] },
  js.configs.recommended,
  ...tseslint.configs.recommended
];
