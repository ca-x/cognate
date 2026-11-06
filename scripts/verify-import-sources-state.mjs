import assert from "node:assert/strict"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"
import { pathToFileURL } from "node:url"

const { rolldown } = await import("../web/node_modules/rolldown/dist/index.mjs")
const input = "import-sources-state-test"
const directory = mkdtempSync(join(tmpdir(), "import-sources-state-"))
const build = await rolldown({ input, plugins: [{ name: input, resolveId: (id) => id === input ? id : undefined, load: (id) => id === input ? `export * from ${JSON.stringify(resolve("web/src/import-selection-state.ts"))}; export { sourcePathLabel } from ${JSON.stringify(resolve("web/src/import-selection-presentation.ts"))}; export * from ${JSON.stringify(resolve("web/src/server-github-proxy-state.ts"))}; export { GitHubProxySettingsSchema } from ${JSON.stringify(resolve("web/src/gen/skillhub/management/v1/github_proxy_pb.ts"))}; export * from ${JSON.stringify(resolve("web/src/gen/skillhub/import/v1/import_pb.ts"))}; export { create } from ${JSON.stringify(resolve("web/node_modules/@bufbuild/protobuf/dist/esm/index.js"))}` : undefined }] })
try {
  const destination = join(directory, "state.mjs")
  await build.write({ file: destination, format: "esm" })
  const state = await import(pathToFileURL(destination))
  const { ImportCandidateStatus: Status, ImportReceiptPhase: Phase, ImportRepositoryOutcomeCode: Outcome } = state
  const candidateId = (value) => value.toString(16).padStart(64, "0")
  const operation = new Uint8Array(16).fill(7)
  const reference = { repository: "synthetic/skills", branch: "main", logicalPath: "skills/review", safeName: "Review" }
  const binding = { repositoryId: "verified-repository", archiveHash: "a".repeat(64), skillId: "verified-skill", contentHash: "b".repeat(64), repository: reference.repository, branch: reference.branch, logicalPath: reference.logicalPath }
  const preview = (path = reference.logicalPath, name = reference.safeName) => state.create(state.PreviewImportSelectionResponseSchema, {
    ticket: new Uint8Array(290), expiresMs: BigInt(Date.now() + 240000),
    repositories: [{ id: candidateId(1), status: Status.READY, hint: { repository: reference.repository, branch: reference.branch } }],
    skills: [{ id: candidateId(2), status: Status.SOURCE_REFERENCE_READY, safeName: name, repositoryCandidateId: candidateId(1), sourceReference: { ...reference, logicalPath: path, safeName: name }, notices: [{ id: candidateId(3), code: 4, count: 1 }] }],
  })
  const ready = (response = preview()) => state.withSelectionPreview(state.initialSelectionState(true), response)
  let cold = ready()
  assert.equal(cold.registerSources, true)
  cold = state.chooseSelection(cold, "skills", candidateId(2), true)
  assert.deepEqual(cold.chosen.skills, [candidateId(2)], "cold source references must be selectable")
  assert.deepEqual(cold.chosen.repositories, [], "skill-only import must not require selecting its repository")
  assert.equal(state.canConfirmSelection(cold), false, "source registration must not bypass acknowledgements")
  cold = state.acknowledgeSelection(cold, candidateId(3), true)
  assert.equal(state.canConfirmSelection(cold), true)
  const plan = state.selectionPlan(cold)
  assert.equal(plan.registerSources, true, "registration mode must be frozen into the operation plan")
  cold.registerSources = false
  cold.preview.skills[0].sourceReference.safeName = "Changed after confirmation"
  assert.equal(plan.registerSources, true)
  assert.equal(plan.preview.skills[0].sourceReference.safeName, "Review", "plan must not alias mutable preview fields")
  const receipt = (selectedPlan = plan) => state.create(state.ImportReceiptSchema, {
    operationId: operation, revision: 1n, phase: Phase.COMPLETED,
    repositoryOutcomes: selectedPlan.repositoryIds.map((id) => ({ candidateId: id, code: Outcome.REGISTERED, repositoryId: "saved-source" })),
    selectedSkillSources: selectedPlan.skillIds.map((id) => ({ ...selectedPlan.preview.skills.find((entry) => entry.id === id).sourceReference, sourceId: "saved-source" })),
  })
  assert.equal(state.acceptSelectionReceipt(receipt(), operation, plan).selectedSkillSources[0].sourceId, "saved-source")
  assert.equal(state.acceptSelectionReceipt(receipt(), operation, plan).selectedSkillBindings.length, 0, "unfetched references must never be verified bindings")
  for (const mutate of [
    (value) => { value.selectedSkillSources[0].sourceId = "" },
    (value) => { value.selectedSkillSources[0].repository = "foreign/skills" },
    (value) => { value.selectedSkillSources[0].branch = "other" },
    (value) => { value.selectedSkillSources[0].logicalPath = "other/path" },
    (value) => { value.selectedSkillSources[0].safeName = "Other name" },
    (value) => { value.selectedSkillSources[0].$unknown = [{ no: 99, wireType: 0, data: new Uint8Array([1]) }] },
    (value) => { value.selectedSkillBindings = [state.create(state.SkillBindingSchema, binding)] },
    (value) => { value.selectedSkillSources = [] },
    (value) => { value.selectedSkillSources.push(value.selectedSkillSources[0]) },
    (value) => { value.phase = Phase.RESERVED },
    (value) => { value.phase = Phase.COLLECTING },
    (value) => { value.phase = Phase.PARTIAL },
    (value) => { value.phase = Phase.FAILED },
  ]) {
    const invalid = receipt()
    mutate(invalid)
    assert.throws(() => state.acceptSelectionReceipt(invalid, operation, plan), "tampered source receipts must fail closed")
  }
  assert.throws(() => state.acceptSelectionReceipt(receipt(), operation, { ...plan, registerSources: false }))
  assert.throws(() => state.withSelectionPreview(state.initialSelectionState(), preview()), "legacy mode must not accept source-only candidates")
  for (const mutate of [
    (value) => { value.skills[0].binding = state.create(state.SkillBindingSchema, binding) },
    (value) => { value.skills[0].sourceReference = undefined },
    (value) => { value.skills[0].sourceReference.sourceId = "premature-id" },
    (value) => { value.skills[0].repositoryCandidateId = "" },
    (value) => { value.skills[0].sourceReference.repository = "other/repo" },
    (value) => { value.skills[0].sourceReference.branch = "other" },
    (value) => { value.skills[0].sourceReference.safeName = "Other name" },
    (value) => { value.skills[0].status = Status.READY },
    (value) => { value.skills[0].status = Status.UNRESOLVED },
  ]) {
    const invalid = preview()
    mutate(invalid)
    assert.throws(() => ready(invalid), "source references must match the candidate and its repository")
  }
  for (const path of ["", ".", "skills/审查", "nested/skill.name"]) {
    let selected = ready(preview(path))
    assert.equal(selected.preview.skills[0].sourceReference.logicalPath, path)
    selected = state.chooseSelection(selected, "skills", candidateId(2), true)
    selected = state.acknowledgeSelection(selected, candidateId(3), true)
    const selectedPlan = state.selectionPlan(selected)
    assert.equal(state.acceptSelectionReceipt(receipt(selectedPlan), operation, selectedPlan).selectedSkillSources[0].logicalPath, path)
    if (path === "" || path === ".") {
      const changed = receipt(selectedPlan)
      changed.selectedSkillSources[0].logicalPath = path === "" ? "." : ""
      assert.throws(() => state.acceptSelectionReceipt(changed, operation, selectedPlan), "unknown paths and explicit repository roots are not interchangeable")
    }
  }
  assert.equal(state.sourcePathLabel(""), "同步后识别路径")
  assert.equal(state.sourcePathLabel("."), "仓库根目录")
  assert.equal(state.sourcePathLabel("skills/review"), "skills/review")
  assert.equal(state.sourcePathLabel("同步技能", () => "translated"), "同步技能", "repository paths must not be translated as UI copy")
  for (const path of ["/absolute", "..", "../skill", "a/../b", "a/./b", "a//b", "a\\b", "a/", "a\0b", "a\u0085b", "\uD800"]) assert.throws(() => ready(preview(path)))
  for (const name of ["a".repeat(256), "界".repeat(85) + "a"]) assert.equal(ready(preview("", name)).preview.skills[0].safeName, name)
  for (const name of ["a".repeat(257), "界".repeat(86), "bad\0name", "\uD800"]) assert.throws(() => ready(preview("", name)))

  let repoOnly = ready()
  repoOnly = state.chooseSelection(repoOnly, "repositories", candidateId(1), true)
  repoOnly = state.acknowledgeSelection(repoOnly, candidateId(3), true)
  const repoPlan = state.selectionPlan(repoOnly)
  assert.deepEqual(repoPlan.skillIds, [])
  for (const code of [Outcome.REGISTERED, Outcome.ALREADY_REGISTERED]) {
    const registered = receipt(repoPlan)
    registered.repositoryOutcomes[0].code = code
    assert.equal(state.acceptSelectionReceipt(registered, operation, repoPlan).repositoryOutcomes[0].repositoryId, "saved-source")
  }
  const both = { ...plan, repositoryIds: repoPlan.repositoryIds }
  assert.equal(state.acceptSelectionReceipt(receipt(both), operation, both).selectedSkillSources.length, 1)
  for (const mutate of [
    (value) => { value.repositoryOutcomes[0].repositoryId = "different-source" },
    (value) => { value.repositoryOutcomes[0].repositoryId = undefined },
    (value) => { value.repositoryOutcomes[0].code = Outcome.COLLECTED },
    (value) => { value.repositoryOutcomes[0].code = Outcome.SOURCE_REJECTED },
  ]) {
    const invalid = receipt(both)
    mutate(invalid)
    assert.throws(() => state.acceptSelectionReceipt(invalid, operation, both))
  }
  for (const sameSource of [true, false]) {
    const multiplePreview = preview()
    const secondReference = { ...reference, repository: sameSource ? reference.repository : "synthetic/other-skills", logicalPath: ".", safeName: "Second skill" }
    if (!sameSource) multiplePreview.repositories.push(state.create(state.ImportRepositoryCandidateSchema, { id: candidateId(5), status: Status.READY, hint: { repository: secondReference.repository, branch: secondReference.branch } }))
    multiplePreview.skills.push(state.create(state.ImportSkillCandidateSchema, { id: candidateId(6), status: Status.SOURCE_REFERENCE_READY, safeName: secondReference.safeName, repositoryCandidateId: candidateId(sameSource ? 1 : 5), sourceReference: secondReference }))
    let multiple = ready(multiplePreview)
    for (const id of [candidateId(2), candidateId(6)]) multiple = state.chooseSelection(multiple, "skills", id, true)
    multiple = state.acknowledgeSelection(multiple, candidateId(3), true)
    const multiplePlan = state.selectionPlan(multiple)
    const multipleReceipt = receipt(multiplePlan)
    if (!sameSource) multipleReceipt.selectedSkillSources[1].sourceId = "other-source"
    assert.equal(state.acceptSelectionReceipt(multipleReceipt, operation, multiplePlan).selectedSkillSources.length, 2)
    multipleReceipt.selectedSkillSources[1].sourceId = sameSource ? "other-source" : "saved-source"
    assert.throws(() => state.acceptSelectionReceipt(multipleReceipt, operation, multiplePlan), "references must share IDs only when they belong to the same source")
  }
  const mixedPreview = preview()
  mixedPreview.skills.push(state.create(state.ImportSkillCandidateSchema, { id: candidateId(4), status: Status.READY, safeName: "Verified", binding }))
  let mixed = ready(mixedPreview)
  for (const id of [candidateId(4), candidateId(2)]) mixed = state.chooseSelection(mixed, "skills", id, true)
  mixed = state.acknowledgeSelection(mixed, candidateId(3), true)
  const mixedPlan = state.selectionPlan(mixed)
  const mixedReceipt = state.create(state.ImportReceiptSchema, { operationId: operation, revision: 1n, phase: Phase.COMPLETED, selectedSkillBindings: [binding], selectedSkillSources: [{ ...reference, sourceId: "saved-source" }] })
  assert.equal(state.acceptSelectionReceipt(mixedReceipt, operation, mixedPlan).selectedSkillBindings.length, 1)

  const legacyResponse = state.create(state.PreviewImportSelectionResponseSchema, { ticket: new Uint8Array(290), expiresMs: BigInt(Date.now() + 240000), skills: [{ id: candidateId(4), status: Status.READY, safeName: "Verified", binding }] })
  let legacy = state.withSelectionPreview(state.initialSelectionState(), legacyResponse)
  delete legacy.registerSources
  legacy = state.chooseSelection(legacy, "skills", candidateId(4), true)
  const legacyPlan = state.selectionPlan(legacy)
  delete legacyPlan.registerSources
  const legacyReceipt = state.create(state.ImportReceiptSchema, { operationId: operation, revision: 1n, phase: Phase.COMPLETED, selectedSkillBindings: [binding] })
  assert.equal(state.acceptSelectionReceipt(legacyReceipt, operation, legacyPlan).selectedSkillBindings.length, 1)
  for (const mutate of [
    (value) => { value.selectedSkillBindings[0].contentHash = "c".repeat(64) },
    (value) => { value.selectedSkillBindings[0].archiveHash = "bad" },
    (value) => { value.selectedSkillBindings = [] },
    (value) => { value.selectedSkillSources = [{ ...reference, sourceId: "saved-source" }] },
  ]) {
    const invalid = structuredClone(legacyReceipt)
    mutate(invalid)
    assert.throws(() => state.acceptSelectionReceipt(invalid, operation, legacyPlan), "verified legacy binding checks must stay strict")
  }
  const unknown = { ...ready(), phase: "unknown" }
  assert.equal(state.canConfirmSelection(unknown), false)
  assert.equal(state.chooseSelection(unknown, "skills", candidateId(2), true), unknown)
  assert.throws(() => state.selectionPlan(unknown))
  const status = state.create(state.GetImportCommitStatusResponseSchema, { observation: state.ImportCommitObservation.NOT_OBSERVED })
  assert.equal(state.acceptSelectionStatus(status, operation, plan), undefined)
  status.observation = state.ImportCommitObservation.TERMINAL
  status.receipt = receipt()
  assert.equal(state.acceptSelectionStatus(status, operation, plan).phase, Phase.COMPLETED)
  status.observation = state.ImportCommitObservation.IN_PROGRESS
  assert.throws(() => state.acceptSelectionStatus(status, operation, plan))
  console.log("Import sources state PASS: cold/skill-only/repo-only, path semantics, UTF-8 names, sealed plan, strict receipts, legacy bindings and no-blind-replay gates")

  for (const value of ["", "http://proxy.example:8080", "https://proxy.example:443", "socks5://proxy.example:1080", "socks5h://user:SYNTHETIC-PASSWORD@proxy.example:1080", "http://[::1]:8080"]) assert.equal(state.validGitHubProxyInput(value), true)
  for (const value of ["ftp://proxy.example", "https://", "https://proxy.example/path", "http://proxy.example?", "http://proxy.example#", "http://proxy.example:0", "socks5://proxy.example:65536", "socks5h://proxy.example:abc", " http://proxy.example", "http://proxy.example\n", "http://proxy.example\\path", "a".repeat(4097)]) assert.equal(state.validGitHubProxyInput(value), false, `invalid synthetic proxy URL admitted: ${value.slice(0, 50)}`)
  const proxySettings = state.create(state.GitHubProxySettingsSchema, { revision: 1n })
  assert.equal(state.acceptGitHubProxySettings(proxySettings).configured, false)
  for (const endpoint of ["http://proxy.example:8080", "https://proxy.example", "socks5://proxy.example:1080", "socks5h://proxy.example:1080"]) assert.equal(state.acceptGitHubProxySettings({ ...proxySettings, configured: true, endpoint }).endpoint, endpoint)
  for (const mutate of [
    (value) => { value.revision = 0n },
    (value) => { value.configured = true },
    (value) => { value.endpoint = "http://proxy.example" },
    (value) => { value.configured = true; value.endpoint = "http://user:SYNTHETIC-PASSWORD@proxy.example" },
    (value) => { value.configured = true; value.endpoint = "http://proxy.example/path" },
    (value) => { value.$unknown = [{ no: 99, wireType: 0, data: new Uint8Array([1]) }] },
  ]) {
    const invalid = structuredClone(proxySettings)
    mutate(invalid)
    assert.throws(() => state.acceptGitHubProxySettings(invalid), "credential-bearing or invalid proxy settings must never reach the UI/cache")
  }
  assert.throws(() => state.acceptGitHubProxySettings(proxySettings, 1n), "confirmed updates must advance the revision")
  assert.equal(state.acceptGitHubProxySettings({ ...proxySettings, revision: 2n }, 1n).revision, 2n)
  console.log("GitHub proxy state PASS: supported protocols, environment reset, credential-free endpoints and CAS revision checks")
} finally {
  await build.close()
  rmSync(directory, { recursive: true, force: true })
}
