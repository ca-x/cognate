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
