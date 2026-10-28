import { t } from "../../i18n"
import { useTranslation } from "react-i18next"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import { X } from "lucide-react"
import type { ComponentProps } from "react"
import { cn } from "../../lib/utils"
import { Button } from "./button"

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

export function DialogContent({ className, children, ...props }: ComponentProps<typeof DialogPrimitive.Content>) {
  useTranslation()
  return <DialogPrimitive.Portal>
    <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-foreground/40" />
    <DialogPrimitive.Content data-slot="dialog-content" className={cn("fixed left-1/2 top-1/2 z-50 max-h-[calc(100svh-2rem)] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-xl border border-border bg-card p-5 shadow-xl sm:p-6", className)} {...props}>
      {children}
      <DialogPrimitive.Close asChild><Button variant="ghost" className="absolute right-2 top-2 size-11 px-0" aria-label={t("关闭对话框")}><X size={18} aria-hidden="true" /></Button></DialogPrimitive.Close>
    </DialogPrimitive.Content>
  </DialogPrimitive.Portal>
}

export function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  useTranslation()
  return <DialogPrimitive.Title className={cn("pr-9 text-lg font-semibold tracking-tight", className)} {...props} />
}

export function DialogDescription({ className, ...props }: ComponentProps<typeof DialogPrimitive.Description>) {
  useTranslation()
  return <DialogPrimitive.Description className={cn("mt-2 pr-4 text-sm leading-6 text-muted-foreground", className)} {...props} />
}
