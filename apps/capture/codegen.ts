import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: "../../services/inspection/schema.graphqls",
  documents: ["src/graphql/documents/**/*.graphql"],
  generates: {
    "src/graphql/schema.ts": { plugins: ["typescript"], config: { avoidOptionals: true, enumsAsTypes: true, scalars: { JSON: "Record<string, unknown>" } } },
    "src/graphql/generated.ts": { plugins: [{ add: { content: 'import type * as Schema from "./schema";' } }, "typescript-operations"], config: { avoidOptionals: true, enumsAsTypes: true, importOperationTypesFrom: "./schema", namespacedImportName: "Schema", scalars: { JSON: "Record<string, unknown>" } } }
  }
};

export default config;
