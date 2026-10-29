export function planGeminiProvider(desired) {
  const apiKey = desired.authMode === "api-key";
  const result = {
    version: 1,
    target: "gemini.settings",
    authMode: desired.authMode,
    selectedType: apiKey ? "gemini-api-key" : "oauth-personal",
    requiredSlots: apiKey ? ["gemini.api-key"] : [],
    localAuthPolicy: "preserve-local-login-v1"
  };
  if (desired.endpoint !== undefined) result.endpoint = desired.endpoint;
  if (desired.keyTransport !== undefined) result.keyTransport = desired.keyTransport;
  if (desired.envModel !== undefined) result.envModel = desired.envModel;
  if (desired.settingsModel !== undefined) result.settingsModel = desired.settingsModel;
  if (apiKey) result.credentialReference = "gemini.api-key";
  return result;
}
