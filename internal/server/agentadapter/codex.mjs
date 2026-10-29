export function planCodexProvider(desired) {
  const result = {
    apiVersion: 1,
    target: "codex.config",
    providerID: "cognate",
    wireAPI: "responses",
    endpoint: desired.endpoint,
    model: desired.model,
    credentialReference: "codex.provider-key",
    requiredSlots: ["codex.provider-key"],
    localAuthPolicy: "preserve-local-login-v1"
  };
  if (desired.reasoningEffort !== undefined) result.reasoningEffort = desired.reasoningEffort;
  if (desired.catalog !== undefined) result.catalog = desired.catalog;
  return result;
}
