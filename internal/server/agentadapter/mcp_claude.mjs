export function planMCP(input) {
  const operations = [];
  for (const server of input.document.servers) {
    const operation = {
      action: server.enabled ? "upsert-managed-server" : "disable-managed-server",
      serverID: server.id,
      name: server.name,
      managedOnly: true
    };
    if (server.enabled) {
      if (server.stdio !== undefined) operation.stdio = server.stdio;
      if (server.http !== undefined) operation.http = server.http;
      if (server.sse !== undefined) operation.sse = server.sse;
    }
    operations.push(operation);
  }
  return {version: 1, target: "claude.mcp", operations};
}
