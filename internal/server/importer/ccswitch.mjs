export function listRepos(rows) {
  return rows.map((row) => ({
    owner: row.owner,
    name: row.name,
    branch: row.branch.toUpperCase() === "HEAD" ? "" : row.branch,
    enabled: row.enabled,
  }));
}

export function listInstalledSkills(rows) {
  return rows.map((row) => ({ id: row.id, name: row.name, directory: row.directory }));
}
