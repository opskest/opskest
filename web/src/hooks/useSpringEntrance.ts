import { useEffect, type RefObject } from 'react'

/** A damped spring, sampled once. Native animation keeps it off React's render path. */
const frames = Array.from({ length: 41 }, (_, index) => {
  const t = index / 40
  const displacement = Math.exp(-8 * t) * (Math.cos(11 * t) + (8 / 11) * Math.sin(11 * t))
  return { transform: `translateY(${12 * displacement}px) scale(${1 - .018 * displacement})`, opacity: Math.min(1, .85 + t * 2), offset: t }
})
frames[40] = { transform: 'translateY(0px) scale(1)', opacity: 1, offset: 1 }

export function useSpringEntrance(ref: RefObject<HTMLElement>, open: boolean) {
  useEffect(() => {
    const element = ref.current
    if (!open || !element || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const animation = element.animate(frames, { duration: 420, easing: 'linear' })
    return () => animation.cancel()
  }, [ref, open])
}
