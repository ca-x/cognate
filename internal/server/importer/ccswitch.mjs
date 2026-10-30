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

function copySelectionValue(value) {
  if (Array.isArray(value)) return value.map(copySelectionValue);
  if (value !== null && typeof value === "object") {
    const result = {};
    for (const key of Object.keys(value)) {
      if (key === "contextWindow" && typeof value[key] !== "string") throw new Error("invalid catalog transport");
      result[key] = copySelectionValue(value[key]);
    }
    return result;
  }
  return value;
}

export function previewSelection(input) {
  return {
    source: input.source,
    sourceDigest: input.sourceDigest,
    repositories: input.repositories.map(copySelectionValue),
    skills: input.skills.map(copySelectionValue),
    configurations: input.configurations.map(copySelectionValue),
    notices: input.notices.map(copySelectionValue),
  };
}
