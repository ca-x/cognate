import type { ComponentProps } from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "../../lib/utils"

const buttonVariants = cva("button inline-flex min-h-11 items-center justify-center gap-2 rounded-lg px-4 text-sm font-medium disabled:pointer-events-none disabled:opacity-50", {
  variants: {
    variant: {
      default: "bg-primary text-primary-foreground",
      outline: "border border-border bg-card text-foreground",
      ghost: "text-muted-foreground",
    },
  },
  defaultVariants: { variant: "default" },
})

export function Button({ className, variant, type = "button", ...props }: ComponentProps<"button"> & VariantProps<typeof buttonVariants>) {
  return <button type={type} data-slot="button" className={cn(buttonVariants({ variant, className }))} {...props} />
}
