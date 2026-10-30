export function previewLibrary(skills) {
  const repositories = [];
  const seen = new Set();
  for (const skill of skills) {
    const source = skill.source;
    if (!source || source.kind !== "github") continue;
    const ref = source.ref || "";
    const key = source.repo + "\n" + ref;
    if (seen.has(key)) continue;
    seen.add(key);
    repositories.push({ repo: source.repo, ref });
  }
  return { skills, repositories };
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
