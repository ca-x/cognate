export function render(input) {
  const env = Object.create(null);
  for (const key of Object.keys(input.providerEnv)) env[key] = input.providerEnv[key];
  return {target: "claude.settings", env};
}

export function planClaudeProvider(input) {
  const operations = [
    {slot: "ANTHROPIC_API_KEY", action: "remove"},
    {slot: "ANTHROPIC_AUTH_TOKEN", action: "remove"},
    {slot: "ANTHROPIC_BASE_URL", action: "remove"},
    {slot: "ANTHROPIC_MODEL", action: "remove"},
    {slot: "ANTHROPIC_DEFAULT_HAIKU_MODEL", action: "remove"},
    {slot: "ANTHROPIC_DEFAULT_SONNET_MODEL", action: "remove"},
    {slot: "ANTHROPIC_DEFAULT_OPUS_MODEL", action: "remove"}
  ];
  if (input.authMode === "api-key") {
    operations[0] = {slot: "ANTHROPIC_API_KEY", action: "set-selected-secret", reference: "claude.api-key"};
  } else if (input.authMode === "auth-token") {
    operations[1] = {slot: "ANTHROPIC_AUTH_TOKEN", action: "set-selected-secret", reference: "claude.auth-token"};
  }
  const values = [input.endpoint, input.models.default, input.models.haiku, input.models.sonnet, input.models.opus];
  for (let index = 0; index < values.length; index++) {
    if (values[index] !== undefined) {
      operations[index + 2] = {slot: operations[index + 2].slot, action: "set-literal", literal: values[index]};
    }
  }
  return {target: "claude.settings", operations};
}
