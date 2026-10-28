import * as SelectPrimitive from "@radix-ui/react-select"
import { Check, ChevronDown, ChevronUp } from "lucide-react"
import type { ComponentProps } from "react"
import { cn } from "../../lib/utils"

export const Select = SelectPrimitive.Root
export const SelectValue = SelectPrimitive.Value

export function SelectTrigger({ className, children, ...props }: ComponentProps<typeof SelectPrimitive.Trigger>) {
  return <SelectPrimitive.Trigger className={cn("inline-flex min-h-11 min-w-0 items-center justify-between gap-2 rounded-lg border border-border bg-card px-3 text-sm text-foreground disabled:opacity-50", className)} {...props}>{children}<SelectPrimitive.Icon asChild><ChevronDown size={15} aria-hidden="true" /></SelectPrimitive.Icon></SelectPrimitive.Trigger>
}

export function SelectContent({ children, ...props }: ComponentProps<typeof SelectPrimitive.Content>) {
  return <SelectPrimitive.Portal><SelectPrimitive.Content className="relative z-50 max-h-72 min-w-36 overflow-hidden rounded-lg border border-border bg-card text-foreground shadow-lg" {...props}><SelectPrimitive.ScrollUpButton className="flex h-7 items-center justify-center"><ChevronUp size={15} aria-hidden="true" /></SelectPrimitive.ScrollUpButton><SelectPrimitive.Viewport className="p-1">{children}</SelectPrimitive.Viewport><SelectPrimitive.ScrollDownButton className="flex h-7 items-center justify-center"><ChevronDown size={15} aria-hidden="true" /></SelectPrimitive.ScrollDownButton></SelectPrimitive.Content></SelectPrimitive.Portal>
}

export function SelectItem({ className, children, ...props }: ComponentProps<typeof SelectPrimitive.Item>) {
  return <SelectPrimitive.Item className={cn("relative flex min-h-11 cursor-default select-none items-center rounded-md py-2 pl-8 pr-3 text-sm outline-none data-[highlighted]:bg-muted data-[disabled]:opacity-50", className)} {...props}><span className="absolute left-2 flex items-center"><SelectPrimitive.ItemIndicator><Check size={14} aria-hidden="true" /></SelectPrimitive.ItemIndicator></span><SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText></SelectPrimitive.Item>
}
