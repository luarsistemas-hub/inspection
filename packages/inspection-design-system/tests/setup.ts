if (!globalThis.CSS) {
  Object.defineProperty(globalThis, "CSS", { value: {} });
}

if (!globalThis.CSS.escape) {
  Object.defineProperty(globalThis.CSS, "escape", {
    value: (value: string) => value.replace(/[^a-zA-Z0-9_-]/g, "\\$&")
  });
}
