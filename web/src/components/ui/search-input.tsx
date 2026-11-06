import { useRef, type ComponentProps } from "react"
import { Search, X } from "lucide-react"
import { cn } from "../../lib/utils"
import { Button } from "./button"
import { Input } from "./input"

type SearchInputProps = Omit<ComponentProps<"input">, "value" | "onChange" | "type" | "ref"> & {
  value: string
  onValueChange: (value: string) => void
  clearLabel: string
}

export function SearchInput({ value, onValueChange, clearLabel, className, disabled, readOnly, onKeyDown, ...props }: SearchInputProps) {
  const input = useRef<HTMLInputElement>(null)
  function clear() {
    onValueChange("")
    input.current?.focus({ preventScroll: true })
  }
  return <div className="relative min-w-0">
    <Search size={16} className="pointer-events-none absolute left-3 top-3.5 text-muted-foreground" aria-hidden="true" />
    <Input {...props} ref={input} type="text" role="searchbox" autoComplete="off" value={value} disabled={disabled} readOnly={readOnly} className={cn("pl-10 pr-12", className)} onChange={(event) => onValueChange(event.target.value)} onKeyDown={(event) => {
      onKeyDown?.(event)
      if (!event.defaultPrevented && event.key === "Escape" && !event.nativeEvent.isComposing && value && !disabled && !readOnly) {
        event.preventDefault()
        event.stopPropagation()
        clear()
      }
    }} />
    {value && !readOnly && <Button variant="ghost" className="absolute right-0 top-0 size-11 px-0" disabled={disabled} aria-label={clearLabel} onClick={clear}><X size={16} aria-hidden="true" /></Button>}
  </div>
}
