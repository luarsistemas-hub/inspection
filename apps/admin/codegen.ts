import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: "../../services/inspection/schema.graphqls",
  documents: ["src/features/**/*.graphql"],
  generates: {
    "src/graphql/schema.ts": { plugins: ["typescript"], config: { avoidOptionals: true, enumsAsTypes: true, scalars: { JSON: "Record<string, unknown>" } } },
    "src/graphql/generated.ts": { plugins: [{ add: { content: 'import type * as Schema from "./schema";\nimport type * as Types from "./generated";' } }, "typescript-operations", "typed-document-node"], config: { avoidOptionals: true, enumsAsTypes: true, importOperationTypesFrom: "./schema", namespacedImportName: "Schema", scalars: { JSON: "Record<string, unknown>" } } }
  }
};

export default config;
