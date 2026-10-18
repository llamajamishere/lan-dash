import { useEffect, useState } from 'react'

/** Current unix time in seconds, re-rendering every `everyMs`. */
export function useNow(everyMs = 1000): number {
  const [now, setNow] = useState(() => Date.now() / 1000)
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now() / 1000), everyMs)
    return () => window.clearInterval(t)
  }, [everyMs])
  return now
}
