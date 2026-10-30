import { t } from "./i18n"
import { ChoiceField, OptionalGroup, MapFields, OrderedFields, TextField, type EditorProps } from "./configuration-opencode-common-fields"
import { openCodeSlots, type ValueFields, type ExtensionFields, type OptionalInput } from "./configuration-opencode-state"

export function OpenCodeValueFields({ value, onChange, path, disabled, depth = 1, referenceOnly = false, objectOnly = false }: EditorProps<ValueFields> & { depth?: number; referenceOnly?: boolean; objectOnly?: boolean }) {
  const kinds = referenceOnly ? ["reference"] : objectOnly ? ["object"] : ["null", "boolean", "number", "array", "object", "reference"]
  function choose(kind: string) {
    if (kind === "boolean") onChange({ kind, value: false })
    else if (kind === "number" || kind === "reference") onChange({ kind, value: "" })
    else if (kind === "array" || kind === "object") onChange({ kind, value: [] })
    else onChange({ kind: kind === "null" ? "null" : "choose" })
  }
  return <div className="min-w-0 space-y-3"><ChoiceField value={value.kind === "choose" ? "" : value.kind} choices={kinds} path={`${path}.type`} label={t("值类型 {{field}}", { field: path })} onChange={choose} disabled={disabled} />{value.kind === "boolean" && <ChoiceField path={path} value={String(value.value)} choices={["true", "false"]} onChange={(next) => onChange({ kind: "boolean", value: next === "true" })} disabled={disabled} />}{value.kind === "number" && <TextField path={path} value={value.value} onChange={(next) => onChange({ kind: "number", value: next })} numeric disabled={disabled} />}{value.kind === "reference" && <ChoiceField path={path} value={value.value} choices={openCodeSlots} onChange={(next) => onChange({ kind: "reference", value: next })} disabled={disabled} />}{value.kind === "array" && <OrderedFields path={path} value={value.value} onChange={(next) => onChange({ kind: "array", value: next })} disabled={disabled || depth >= 12} maximum={2048} make={() => ({ kind: "choose" } as ValueFields)} render={(input) => <OpenCodeValueFields {...input} depth={depth + 1} />} />}{value.kind === "object" && <MapFields path={path} value={value.value} onChange={(next) => onChange({ kind: "object", value: next })} disabled={disabled || depth >= 12} make={() => ({ kind: "choose" } as ValueFields)} render={(input) => <OpenCodeValueFields {...input} depth={depth + 1} />} />}</div>
}
export function OpenCodeOptionalValue(props: EditorProps<OptionalInput<ValueFields>> & { referenceOnly?: boolean }) { return <OptionalGroup {...props}><OpenCodeValueFields {...props} value={props.value.value} onChange={(next) => props.onChange({ ...props.value, value: next })} /></OptionalGroup> }
export function OpenCodeObjectFields(props: EditorProps<ExtensionFields> & { extensions?: boolean }) { return <OptionalGroup {...props}><MapFields path={props.path} disabled={props.disabled} value={props.value.value} onChange={(next) => props.onChange({ ...props.value, value: next })} maximum={props.extensions ? 256 : 2048} make={() => ({ kind: "choose" } as ValueFields)} render={(input) => <OpenCodeValueFields {...input} depth={props.extensions ? 1 : 2} />} /></OptionalGroup> }
