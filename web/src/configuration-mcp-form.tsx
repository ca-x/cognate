import { useId, useLayoutEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { clone, create, toJsonString } from "@bufbuild/protobuf"
import { Action, ArgSchema, ArgsSchema, DraftSchema, NamedSlotSchema, NamedSlotsSchema, Readiness, ServerSchema, Warning, type Draft, type PreviewResponse, type SecretMutation, type Server, type View } from "./gen/skillhub/configuration/mcp/v1/mcp_pb"
import { clearMCPMutations, mcpMutations, mcpSlots, validateMCP, type MCPInputs } from "./configuration-mcp-state"
import { Input } from "./components/ui/input"
import { Button } from "./components/ui/button"
import { Checkbox } from "./components/ui/checkbox"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./components/ui/select"

export type MCPFormAttempt = (kind: "preview" | "save", draft: Draft, secrets: SecretMutation[]) => Promise<PreviewResponse | true | undefined>

function TextField({ label, value, disabled, onChange, invalid = false, description }: { label: string; value: string; disabled: boolean; onChange: (value: string) => void; invalid?: boolean; description?: string }) {
  const id = useId()
  return <div className="mcp-field"><label htmlFor={id}>{label}</label><Input id={id} value={value} disabled={disabled} autoComplete="off" autoCapitalize="off" spellCheck={false} aria-invalid={invalid} aria-describedby={description} onChange={event => onChange(event.target.value)} /></div>
}

function Toggle({ label, checked, disabled, onChange }: { label: string; checked: boolean; disabled: boolean; onChange: (value: boolean) => void }) {
  const id = useId()
  return <div className="mcp-toggle"><Checkbox id={id} checked={checked} disabled={disabled} onCheckedChange={value => onChange(value === true)} /><label htmlFor={id}>{label}</label></div>
}

export function MCPConfigurationForm({ original, pending, blocked, secretEpoch, onAttempt, onDirtyChange, onSecrets }: { original?: View; pending: boolean; blocked: boolean; secretEpoch: number; onAttempt: MCPFormAttempt; onDirtyChange: (dirty: boolean) => void; onSecrets: (clear: (() => void) | undefined) => void }) {
  const { t } = useTranslation("configurationMCP")
  const id = useId()
  const [initial] = useState(() => original?.draft ? clone(DraftSchema, original.draft) : create(DraftSchema, { agentId: "claude", document: { servers: [] } }))
  const [draft, setDraft] = useState(initial)
  const [version, setVersion] = useState(0)
  const [review, setReview] = useState<{ version: number; response: PreviewResponse }>()
  const [approved, setApproved] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [message, setMessage] = useState("")
  const [working, setWorking] = useState(false)
  const secretInputs = useRef<MCPInputs>({})
  const passwordNodes = useRef(new Map<string, HTMLInputElement>())
  const bytes = useRef<SecretMutation[]>([])
  const form = useRef<HTMLFormElement>(null)
  const alive = useRef(true)
  const locked = useRef(false)
  const focus = useRef<string | undefined>(undefined)
  const keys = useRef((initial.document?.servers ?? []).map(() => crypto.randomUUID()))
  const disabled = pending || blocked || working
  const previous = original?.secrets ?? []
  const refs = mcpSlots(draft.document)
  const allRefs = [...refs, ...previous.map(slot => slot.slotId).filter(ref => !refs.includes(ref))]
  const dirty = toJsonString(DraftSchema, initial) !== toJsonString(DraftSchema, draft) || Object.values(secretInputs.current).some(input => input.action !== "keep" || input.value !== "" || input.confirmed) || working

  function wipe() {
    for (const value of Object.values(secretInputs.current)) value.value = ""
    secretInputs.current = {}
    for (const node of passwordNodes.current.values()) node.value = ""
    clearMCPMutations(bytes.current)
    bytes.current = []
  }
  useLayoutEffect(() => { alive.current = true; onSecrets(wipe); return () => { alive.current = false; wipe(); onSecrets(undefined) } }, [onSecrets])
  useLayoutEffect(() => { wipe(); setReview(undefined); setApproved(false); setVersion(value => value + 1) }, [secretEpoch])
  useLayoutEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange])
  useLayoutEffect(() => {
    if (focus.current) { form.current?.querySelector<HTMLElement>(focus.current)?.focus(); focus.current = undefined }
  }, [version])
  useLayoutEffect(() => {
    if (Object.keys(errors).length) form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()
  }, [errors])

  function changed(next: Draft) {
    if (disabled) return
    const allowed = new Set([...mcpSlots(next.document), ...previous.map(slot => slot.slotId)])
    for (const ref of Object.keys(secretInputs.current)) if (!allowed.has(ref)) { secretInputs.current[ref].value = ""; delete secretInputs.current[ref] }
    setDraft(next); setVersion(value => value + 1); setApproved(false); setErrors({})
  }
  function updateServer(index: number, update: (server: Server) => void) {
    const next = clone(DraftSchema, draft)
    const server = next.document!.servers[index]
    update(server)
    if (server.connection.case === "stdio") {
      for (const arg of server.connection.value.args?.values ?? []) arg.slotRef = `arg/${server.id}/${arg.id}`
      for (const env of server.connection.value.env?.values ?? []) env.slotRef = `env/${server.id}/${env.name}`
    } else if (server.connection.case === "http" || server.connection.case === "sse") {
      for (const header of server.connection.value.headers?.values ?? []) header.slotRef = `header/${server.id}/${header.name}`
    }
    changed(next)
  }
  function mutateSecret(ref: string, update: Partial<MCPInputs[string]>) {
    if (disabled) return
    const value = { ...(secretInputs.current[ref] ?? { action: "keep", value: "", confirmed: false }), ...update }
    if (update.action !== undefined) { value.value = ""; value.confirmed = false }
    secretInputs.current[ref] = value
    setVersion(current => current + 1); setApproved(false); setErrors({})
  }
  function moveServer(index: number, direction: -1 | 1) {
    const next = clone(DraftSchema, draft), servers = next.document!.servers
    const destination = index + direction
    if (destination < 0 || destination >= servers.length) return
    ;[servers[index], servers[destination]] = [servers[destination], servers[index]]
    ;[keys.current[index], keys.current[destination]] = [keys.current[destination], keys.current[index]]
    focus.current = `[data-mcp-server="${keys.current[destination]}"]`
    setMessage("moved"); changed(next)
  }
  function collection(index: number, kind: "args" | "env" | "headers") {
    const server = draft.document!.servers[index]
    const values = server.connection.case === "stdio" ? kind === "args" ? server.connection.value.args : server.connection.value.env : server.connection.case === "http" || server.connection.case === "sse" ? server.connection.value.headers : undefined
    const set = (update: (rows: { id?: string; name?: string; slotRef: string }[]) => void) => updateServer(index, current => {
      if (current.connection.case === "stdio") {
        if (kind === "args") update(current.connection.value.args!.values)
        else update(current.connection.value.env!.values)
      } else if (current.connection.case === "http" || current.connection.case === "sse") update(current.connection.value.headers!.values)
    })
    const toggle = (enabled: boolean) => updateServer(index, current => {
      if (current.connection.case === "stdio") {
        if (kind === "args") current.connection.value.args = enabled ? create(ArgsSchema) : undefined
        else current.connection.value.env = enabled ? create(NamedSlotsSchema) : undefined
      } else if (current.connection.case === "http" || current.connection.case === "sse") current.connection.value.headers = enabled ? create(NamedSlotsSchema) : undefined
    })
    const label = kind === "args" ? "argId" : kind === "env" ? "envName" : "headerName"
    return <section className="mcp-collection" key={kind}>
      <Toggle label={t(kind === "args" ? "optionalArgs" : kind === "env" ? "optionalEnv" : "optionalHeaders")} checked={values !== undefined} disabled={disabled} onChange={toggle} />
      {values && <><h4>{t(kind)}</h4><ol>{values.values.map((row, position) => <li key={position} tabIndex={-1} data-mcp-row={`${index}-${kind}-${position}`}>
        <TextField label={t(label)} value={"id" in row ? row.id : row.name} disabled={disabled} onChange={text => set(rows => { if (kind === "args") rows[position].id = text; else rows[position].name = text })} />
        <p className="mcp-reference">{t("reference")}: <code>{row.slotRef}</code></p>
        <div className="mcp-actions">{([-1, 1] as const).map(direction => <Button key={direction} type="button" variant="outline" disabled={disabled || position + direction < 0 || position + direction >= values.values.length} onClick={() => { focus.current = `[data-mcp-row="${index}-${kind}-${position + direction}"]`; set(rows => { [rows[position], rows[position + direction]] = [rows[position + direction], rows[position]] }); setMessage("moved") }}>{t(direction < 0 ? "up" : "down")}</Button>)}<Button type="button" variant="ghost" disabled={disabled} onClick={() => { focus.current = `[data-mcp-server="${keys.current[index]}"]`; set(rows => rows.splice(position, 1)) }}>{t("remove")}</Button></div>
      </li>)}</ol><Button type="button" variant="outline" disabled={disabled || values.values.length >= (kind === "headers" ? 32 : 64)} onClick={() => set(rows => rows.push(kind === "args" ? create(ArgSchema) : create(NamedSlotSchema)))}>{t(kind === "args" ? "addArg" : kind === "env" ? "addEnv" : "addHeader")}</Button></>}
    </section>
  }
  async function attempt(kind: "preview" | "save") {
    if (disabled || locked.current || kind === "save" && (!approved || review?.version !== version)) return
    const found = validateMCP(draft)
    if (Object.keys(found).length) { setErrors(found); return }
    let changes: SecretMutation[]
    try { changes = mcpMutations(draft, previous, secretInputs.current) } catch { setErrors({ secrets: "secretError" }); return }
    locked.current = true; bytes.current = changes; setWorking(true)
    try {
      const response = await onAttempt(kind, clone(DraftSchema, draft), changes)
      if (alive.current && response && response !== true) { setReview({ version, response }); setApproved(false) }
    } finally {
      clearMCPMutations(changes); bytes.current = []
      locked.current = false
      if (alive.current) { setWorking(false); if (kind === "save") { wipe(); setReview(undefined); setApproved(false); setVersion(value => value + 1) } }
    }
  }
  return <form ref={form} className="mcp-form" autoComplete="off" onSubmit={event => { event.preventDefault(); void attempt("save") }}>
    <TextField label={t("name")} value={draft.name} disabled={disabled} invalid={!!errors.name} description={errors.name ? `${id}-name-error` : undefined} onChange={name => { const next = clone(DraftSchema, draft); next.name = name; changed(next) }} />
    {errors.name && <p role="alert" id={`${id}-name-error`}>{t(errors.name)}</p>}
    <h3>{t("declaration")}</h3>
    {errors.document && <p role="alert">{t(errors.document)}</p>}
    {(draft.document?.servers ?? []).map((server, index) => <fieldset key={keys.current[index]} tabIndex={-1} data-mcp-server={keys.current[index]} aria-invalid={!!errors[`server-${index}`]} aria-describedby={errors[`server-${index}`] ? `${id}-server-${index}-error` : undefined}>
      <legend>{t("server", { position: index + 1 })}</legend>
      <div className="mcp-grid"><TextField label={t("serverId")} value={server.id} disabled={disabled} onChange={text => updateServer(index, current => { current.id = text })} /><TextField label={t("serverName")} value={server.name} disabled={disabled} onChange={text => updateServer(index, current => { current.name = text })} /></div>
      <Toggle label={t("enabled")} checked={server.enabled === true} disabled={disabled} onChange={enabled => updateServer(index, current => { current.enabled = enabled })} />
      <div className="mcp-field"><label htmlFor={`${id}-transport-${index}`}>{t("transport")}</label><Select value={server.connection.case ?? "stdio"} disabled={disabled} onValueChange={transport => updateServer(index, current => {
        if (transport === "stdio") current.connection = { case: "stdio", value: { $typeName: "skillhub.configuration.mcp.v1.Stdio", command: "" } }
        else if (transport === "http" || transport === "sse") current.connection = { case: transport, value: current.connection.case === "http" || current.connection.case === "sse" ? current.connection.value : { $typeName: "skillhub.configuration.mcp.v1.Remote", url: "" } }
      })}><SelectTrigger id={`${id}-transport-${index}`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="stdio">stdio</SelectItem><SelectItem value="http">HTTP</SelectItem><SelectItem value="sse">SSE</SelectItem></SelectContent></Select></div>
      {server.connection.case === "stdio" ? <><TextField label={t("command")} value={server.connection.value.command} disabled={disabled} onChange={command => updateServer(index, current => { if (current.connection.case === "stdio") current.connection.value.command = command })} />{collection(index, "args")}{collection(index, "env")}</> : server.connection.case === "http" || server.connection.case === "sse" ? <><TextField label={t("url")} value={server.connection.value.url} disabled={disabled} onChange={url => updateServer(index, current => { if (current.connection.case === "http" || current.connection.case === "sse") current.connection.value.url = url })} />{collection(index, "headers")}</> : null}
      {errors[`server-${index}`] && <p role="alert" id={`${id}-server-${index}-error`}>{t(errors[`server-${index}`])}</p>}
      <div className="mcp-actions"><Button type="button" variant="outline" disabled={disabled || index === 0} onClick={() => moveServer(index, -1)}>{t("up")}</Button><Button type="button" variant="outline" disabled={disabled || index === draft.document!.servers.length - 1} onClick={() => moveServer(index, 1)}>{t("down")}</Button><Button type="button" variant="ghost" disabled={disabled} onClick={() => { const next = clone(DraftSchema, draft); next.document!.servers.splice(index, 1); keys.current.splice(index, 1); focus.current = "[data-mcp-add-server]"; changed(next) }}>{t("remove")}</Button></div>
    </fieldset>)}
    <Button data-mcp-add-server type="button" variant="outline" disabled={disabled || draft.document!.servers.length >= 32} onClick={() => { const next = clone(DraftSchema, draft); next.document!.servers.push(create(ServerSchema, { id: "", name: "", enabled: true, connection: { case: "stdio", value: { command: "" } } })); keys.current.push(crypto.randomUUID()); changed(next) }}>{t("addServer")}</Button>
    <fieldset tabIndex={-1} aria-invalid={!!errors.secrets} aria-describedby={`${id}-privacy`}><legend>{t("credentials")}</legend><p id={`${id}-privacy`} className="mcp-help">{t("privacy")}</p>{allRefs.map((ref, index) => {
      const input = secretInputs.current[ref] ?? { action: "keep", value: "", confirmed: false }
      const present = previous.find(slot => slot.slotId === ref)?.present === true
      return <div key={ref} className="mcp-secret" data-mcp-slot={ref}><code>{ref}</code><p>{t(present ? "present" : "absent")}</p>{!refs.includes(ref) && <p>{t("removed")}</p>}<div className="mcp-field"><label htmlFor={`${id}-action-${index}`}>{t("operation")}</label><Select value={input.action} disabled={disabled} onValueChange={action => { if (action === "keep" || action === "replace" || action === "clear") mutateSecret(ref, { action }) }}><SelectTrigger id={`${id}-action-${index}`}><SelectValue /></SelectTrigger><SelectContent><SelectItem value="keep" disabled={!refs.includes(ref)}>{t("keep")}</SelectItem><SelectItem value="replace" disabled={!refs.includes(ref)}>{t("replace")}</SelectItem><SelectItem value="clear">{t("clear")}</SelectItem></SelectContent></Select></div>{input.action === "replace" && <div className="mcp-field"><label htmlFor={`${id}-secret-${index}`}>{t("replacement")}</label><Input ref={node => { if (node) passwordNodes.current.set(ref, node); else passwordNodes.current.delete(ref) }} id={`${id}-secret-${index}`} type="password" autoComplete="new-password" spellCheck={false} disabled={disabled} value={input.value} onChange={event => mutateSecret(ref, { value: event.target.value })} /></div>}{input.action === "clear" && <Toggle label={t("confirmed")} checked={input.confirmed} disabled={disabled} onChange={confirmed => mutateSecret(ref, { confirmed })} />}</div>
    })}{errors.secrets && <p role="alert">{t(errors.secrets)}</p>}</fieldset>
    <p className="mcp-help">{t("previewHelp")}</p><Button type="button" variant="outline" disabled={disabled} onClick={() => void attempt("preview")}>{t("preview")}</Button>
    {review && <section className="mcp-review" aria-label={t("preview")}><p role="status">{t(review.version === version ? "previewReady" : "previewChanged")}</p><p>{t(review.response.readiness === Readiness.COMPLETE ? "complete" : "needs")}</p><ol>{review.response.operations.map(operation => <li key={operation.serverId}>{operation.nativeName}: {t(operation.action === Action.UPSERT_MANAGED ? "upsert" : "disable")}</li>)}</ol>{review.response.warnings.includes(Warning.INSECURE_HTTP) && <p>{t("insecure")}</p>}<p>{t("native")}</p></section>}
    <Toggle label={t("approve")} checked={approved} disabled={disabled || review?.version !== version} onChange={setApproved} />
    <Button type="submit" disabled={disabled || !approved || review?.version !== version}>{t("save")}</Button>
    <p role="status" aria-live="polite">{message && t(message)}</p>
  </form>
}
