import type { ComponentProps } from "react"
import { cn } from "../../lib/utils"

export function Input({ className, ...props }: ComponentProps<"input">) {
  return <input data-slot="input" className={cn("min-h-11 w-full rounded-lg border border-border bg-card px-3 text-base outline-none placeholder:text-muted-foreground disabled:opacity-60", className)} {...props} />
}
