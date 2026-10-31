import { useCallback, type Ref } from "react"

export function useMotionRef<Element extends HTMLElement>(forwardedRef?: Ref<Element>) {
  return useCallback((node: Element | null) => {
    if (node && !node.hasAttribute("data-motion")) {
      node.dataset.motion = document.documentElement.dataset.input === "keyboard" ? "off" : "on"
    }
    if (typeof forwardedRef === "function") return forwardedRef(node)
    if (forwardedRef) forwardedRef.current = node
  }, [forwardedRef])
}
