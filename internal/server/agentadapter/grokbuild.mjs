export function planGrokBuildProvider(desired) {
  const result = {
    version: 1,
    target: "grokbuild.config",
    mode: desired.mode,
    requiredSlots: [],
    localAuthPolicy: "preserve-local-login-v1"
  };
  if (desired.model !== undefined) {
    result.model = { ...desired.model };
    if (desired.model.keyPresent) {
      result.credentialReference = "grokbuild.api-key";
      result.requiredSlots = ["grokbuild.api-key"];
    }
  }
  return result;
}
