import * as CheckboxPrimitive from "@radix-ui/react-checkbox"
import { Check } from "lucide-react"
import type { ComponentProps } from "react"
import { cn } from "../../lib/utils"

export function Checkbox({ className, ...props }: ComponentProps<typeof CheckboxPrimitive.Root>) {
  return <CheckboxPrimitive.Root data-slot="checkbox" className={cn("flex size-5 shrink-0 items-center justify-center rounded border border-border bg-card text-primary-foreground data-[state=checked]:border-primary data-[state=checked]:bg-primary disabled:opacity-50", className)} {...props}>
    <CheckboxPrimitive.Indicator><Check size={14} aria-hidden="true" /></CheckboxPrimitive.Indicator>
  </CheckboxPrimitive.Root>
}
