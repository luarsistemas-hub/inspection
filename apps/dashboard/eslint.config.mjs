import nextPlugin from "@next/eslint-plugin-next";
import tseslint from "typescript-eslint";

export default [
  { ignores: [".next/**", "node_modules/**", "src/graphql/generated.ts"] },
  { plugins: { "@next/next": nextPlugin }, rules: nextPlugin.configs["core-web-vitals"].rules },
  { files: ["**/*.{ts,tsx}"], languageOptions: { parser: tseslint.parser } },
];
