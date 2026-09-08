import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: "../../services/inspection/schema.graphqls",
  documents: ["src/features/**/*.graphql"],
  generates: { "src/graphql/generated.ts": { plugins: ["typescript"], config: { avoidOptionals: true, enumsAsTypes: true, scalars: { JSON: "Record<string, unknown>" } } } }
};

export default config;
