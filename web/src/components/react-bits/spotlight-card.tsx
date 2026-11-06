import { useRef, type ComponentProps } from "react"
import { useMotionRef } from "../../lib/motion"
import { cn } from "../../lib/utils"
import "./effects.css"

type Props = Omit<ComponentProps<"div">, "ref"> & { as?: "div" | "section" | "aside" }

export function SpotlightCard({ as: Surface = "div", children, className, onPointerMove, onPointerLeave, ...props }: Props) {
  const root = useRef<HTMLElement>(null)
  const ref = useMotionRef(root)
  return <Surface {...props} ref={ref} className={cn("bits-spotlight", className)} onPointerMove={(event) => {
    onPointerMove?.(event)
    if (event.pointerType !== "mouse" || document.hidden || document.documentElement.dataset.input === "keyboard" || !window.matchMedia("(hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference)").matches) return
    const surface = root.current
    if (!surface) return
    const bounds = surface.getBoundingClientRect()
    surface.style.setProperty("--spotlight-x", `${event.clientX - bounds.left}px`)
    surface.style.setProperty("--spotlight-y", `${event.clientY - bounds.top}px`)
    surface.dataset.spotlight = "active"
  }} onPointerLeave={(event) => { onPointerLeave?.(event); if (root.current) delete root.current.dataset.spotlight }}>
    <span className="bits-spotlight-light" aria-hidden="true" />
    {children}
  </Surface>
}
