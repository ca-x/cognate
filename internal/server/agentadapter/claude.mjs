export function render(input) {
  const env = Object.create(null);
  for (const key of Object.keys(input.providerEnv)) env[key] = input.providerEnv[key];
  return {target: "claude.settings", env};
}
