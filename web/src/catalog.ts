export const catalogPageSize = 24
export const catalogCategoryPageSize = 64
export const catalogMaxPages = 100
export const ownerCategoryLimit = 100
export const skillCategoryLimit = 8
export const categoryNameMaxBytes = 80

export function queryByteLength(value: string) {
  return new TextEncoder().encode(value).length
}

export function validateCatalogSearch(search: Record<string, unknown>): { q?: string; category?: string } {
  return {
    q: typeof search.q === "string" ? search.q : undefined,
    category: typeof search.category === "string" ? search.category : undefined,
  }
}

export function publicInstallCommand(origin: string, skillId: string) {
  const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`
  return `skillhub install-public --server ${quote(origin)} --skill ${quote(skillId)}`
}
