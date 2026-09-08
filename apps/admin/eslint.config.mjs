import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default [
  { ignores: [".next/**", "node_modules/**", "playwright-report/**", "next-env.d.ts", "src/graphql/generated.ts"] },
  js.configs.recommended,
  ...tseslint.configs.recommended
];
