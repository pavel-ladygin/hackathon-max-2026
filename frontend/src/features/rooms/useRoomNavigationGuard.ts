import { useLayoutEffect, useMemo, useRef } from 'react'
import { useLocation } from 'react-router-dom'
import { launchContext } from '../../shared/platform/max/launchContext'

const browserHistoryKey = () => typeof window === 'undefined' ? undefined : window.history.state?.key

// Requests may finish after the user leaves. Keep their data/cache updates,
// but only continue navigation on the screen where the request was started.
export function useRoomNavigationGuard() {
  const { key } = useLocation()
  const activeKey = useRef<string | null>(null)
  const route = useMemo(() => ({ key, historyKey: browserHistoryKey() }), [key])

  useLayoutEffect(() => {
    activeKey.current = key
    return () => { activeKey.current = null }
  }, [key])

  // BrowserRouter pushes history before React commits a lazy route. Keep the
  // entry captured for this screen even if a query rerenders it during exit.
  const canNavigate = () => activeKey.current === route.key && browserHistoryKey() === route.historyKey
  return {
    canNavigate,
    captureNavigation: () => {
      const launchRevision = launchContext.getSnapshot().revision
      return () => canNavigate() && launchContext.getSnapshot().revision === launchRevision
    },
  }
}
