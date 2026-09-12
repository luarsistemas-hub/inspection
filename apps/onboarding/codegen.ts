import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: "../../services/inspection/schema.graphqls",
  documents: ["src/features/**/*.graphql"],
  generates: {
    "src/graphql/schema.ts": { plugins: ["typescript"], config: { avoidOptionals: true, enumsAsTypes: true, scalars: { JSON: "Record<string, unknown>" } } },
    "src/graphql/generated.ts": { plugins: ["typescript-operations", "typed-document-node"], config: { avoidOptionals: true, enumsAsTypes: true, scalars: { JSON: "Record<string, unknown>" } } },
  },
};

export default config;
