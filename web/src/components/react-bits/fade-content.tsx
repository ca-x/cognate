import type { ComponentProps } from "react"
import { useMotionRef } from "../../lib/motion"
import { cn } from "../../lib/utils"
import "./effects.css"

export function FadeContent({ children, className, ref: forwardedRef, ...props }: ComponentProps<"div">) {
  const ref = useMotionRef(forwardedRef)
  return <div {...props} ref={ref} className={cn("bits-fade-content", className)}>{children}</div>
}
