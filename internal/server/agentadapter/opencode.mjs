export function planOpenCodeProvider(input) {
  return {
    version: 1,
    target: "opencode.config",
    mode: "coexist",
    format: input.format,
    container: input.format === "v1" ? "provider" : "providers",
    providerID: input.providerID,
    definitionRef: "host-validated-definition-v1",
    requiredSlots: input.requiredSlots
  };
}
