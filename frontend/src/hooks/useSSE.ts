import { useEffect } from 'react'
import { useDash } from '../lib/store'
import type { SSEEvent } from '../lib/types'

/**
 * Subscribes to /api/events while mounted. Reconnects with exponential
 * backoff (1s → 30s) and refetches full state after every reconnect so no
 * events are missed during the gap.
 */
export function useSSE(enabled: boolean) {
  const handleEvent = useDash((s) => s.handleEvent)
  const setConnected = useDash((s) => s.setConnected)
  const loadAll = useDash((s) => s.loadAll)

  useEffect(() => {
    if (!enabled) return
    let es: EventSource | null = null
    let retry = 1000
    let timer: number | undefined
    let closed = false
    let first = true

    const connect = () => {
      es = new EventSource('/api/events', { withCredentials: true })
      es.onopen = () => {
        retry = 1000
        setConnected(true)
        if (!first) void loadAll().catch(() => {})
        first = false
      }
      es.onmessage = (msg) => {
        try {
          handleEvent(JSON.parse(msg.data) as SSEEvent)
        } catch {
          /* ignore malformed */
        }
      }
      es.onerror = () => {
        setConnected(false)
        es?.close()
        if (closed) return
        // Session may have expired: a cheap auth probe triggers redirect.
        fetch('/api/status', { credentials: 'same-origin' }).then((r) => {
          if (r.status === 401 || r.status === 428) window.location.assign('/login')
        }).catch(() => {})
        timer = window.setTimeout(connect, retry)
        retry = Math.min(retry * 2, 30000)
      }
    }
    connect()
    return () => {
      closed = true
      window.clearTimeout(timer)
      es?.close()
      setConnected(false)
    }
  }, [enabled, handleEvent, setConnected, loadAll])
}
