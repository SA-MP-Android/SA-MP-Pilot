import { useEffect, useRef, useState } from 'react'

/**
 * Returns a trailing-debounced copy of `value` that changes at most once every
 * `intervalMs`. Fast intermediate values are dropped, so expensive consumers
 * like the nearby tab only re-render at a capped rate.
 */
export function useThrottledValue<T>(value: T, intervalMs: number): T {
  const [displayed, setDisplayed] = useState(value)
  const lastUpdateRef = useRef(0)

  useEffect(() => {
    const now = Date.now()
    const elapsed = now - lastUpdateRef.current
    if (elapsed >= intervalMs) {
      lastUpdateRef.current = now
      setDisplayed(value)
      return
    }
    const timer = window.setTimeout(() => {
      lastUpdateRef.current = Date.now()
      setDisplayed(value)
    }, intervalMs - elapsed)
    return () => window.clearTimeout(timer)
  }, [value, intervalMs])

  return displayed
}
