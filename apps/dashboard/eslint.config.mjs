import tseslint from "typescript-eslint";

export default [{ ignores: [".next/**", "node_modules/**", "src/graphql/generated.ts"] }, { files: ["**/*.{ts,tsx}"], languageOptions: { parser: tseslint.parser } }];
