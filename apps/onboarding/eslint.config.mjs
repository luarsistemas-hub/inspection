import js from "@eslint/js";
import nextPlugin from "@next/eslint-plugin-next";
import tseslint from "typescript-eslint";

export default [
  { ignores: [".next/**", "node_modules/**", "playwright-report/**", "next-env.d.ts"] },
  { plugins: { "@next/next": nextPlugin }, rules: nextPlugin.configs["core-web-vitals"].rules },
  js.configs.recommended,
  ...tseslint.configs.recommended,
];
