import type { CodegenConfig } from "@graphql-codegen/cli";

const config: CodegenConfig = {
  schema: "../../services/inspection/schema.graphqls",
  documents: ["src/graphql/documents/**/*.graphql"],
  generates: {
    "src/graphql/generated.ts": {
      // The schema types are shared by the small hand-written transport while
      // this generation step also validates every .graphql document above.
      plugins: ["typescript"],
      config: {
        avoidOptionals: true,
        preResolveTypes: false,
        enumsAsTypes: true,
        scalars: { JSON: "Record<string, unknown>" }
      }
    }
  }
};

export default config;
