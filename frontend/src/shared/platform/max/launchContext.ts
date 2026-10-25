import { maxPlatform } from './adapter'

export type LaunchContextSnapshot = {
  revision: number;
  initData: string;
  startParam: string | null;
}

type Listener = () => void

const emptySnapshot: LaunchContextSnapshot = { revision: 0, initData: '', startParam: null }

function bridgeInitData() {
  try { return maxPlatform.getInitData() || '' } catch { return '' }
}

function bridgeStartParam() {
  try { return maxPlatform.getStartParam() } catch { return null }
}

function hashInitData() {
  if (typeof window === 'undefined') return ''
  try { return new URLSearchParams(window.location.hash.slice(1)).get('WebAppData') || '' } catch { return '' }
}

export function createLaunchContextStore() {
  const listeners = new Set<Listener>()
  let poll: number | undefined
  let lastBridgeRaw = bridgeInitData()
  let lastHashRaw = hashInitData()
  const initialHint = bridgeStartParam()
  let snapshot: LaunchContextSnapshot = lastHashRaw
    ? { revision: 0, initData: lastHashRaw, startParam: lastHashRaw === lastBridgeRaw ? initialHint : null }
    : { revision: 0, initData: lastBridgeRaw, startParam: initialHint }

  const notify = () => { for (const listener of listeners) listener() }
  const accept = (initData: string, startParam: string | null) => {
    if (snapshot.initData === initData && snapshot.startParam === startParam) return
    snapshot = { revision: snapshot.revision + 1, initData, startParam }
    notify()
  }
  const observe = () => {
    const bridgeRaw = bridgeInitData()
    const hashRaw = hashInitData()
    const hint = bridgeStartParam()
    const bridgeChanged = bridgeRaw !== lastBridgeRaw
    const hashChanged = hashRaw !== lastHashRaw
    lastBridgeRaw = bridgeRaw
    lastHashRaw = hashRaw

    if (hashRaw && hashChanged) {
      accept(hashRaw, null)
    } else if (bridgeRaw && bridgeChanged) {
      accept(bridgeRaw, hint)
    } else if (!hashRaw && !bridgeRaw && !snapshot.initData && hint !== snapshot.startParam) {
      // Preserve browser-only launch links, which have no signed MAX payload.
      accept('', hint)
    }
  }
  const visible = () => typeof document === 'undefined' || document.visibilityState !== 'hidden'
  const startPolling = () => {
    if (typeof window !== 'undefined' && poll === undefined && visible()) poll = window.setInterval(observe, 500)
  }
  const onSignal = () => {
    if (visible()) {
      observe()
      startPolling()
    } else if (poll !== undefined) {
      window.clearInterval(poll)
      poll = undefined
    }
  }
  const start = () => {
    if (typeof window === 'undefined') return
    window.addEventListener('focus', onSignal)
    window.addEventListener('pageshow', onSignal)
    window.addEventListener('hashchange', onSignal)
    window.addEventListener('popstate', onSignal)
    document.addEventListener('visibilitychange', onSignal)
    observe()
    startPolling()
  }
  const stop = () => {
    if (typeof window === 'undefined') return
    window.removeEventListener('focus', onSignal)
    window.removeEventListener('pageshow', onSignal)
    window.removeEventListener('hashchange', onSignal)
    window.removeEventListener('popstate', onSignal)
    document.removeEventListener('visibilitychange', onSignal)
    if (poll !== undefined) window.clearInterval(poll)
    poll = undefined
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener: Listener) {
      listeners.add(listener)
      if (listeners.size === 1) start()
      return () => {
        listeners.delete(listener)
        if (listeners.size === 0) stop()
      }
    },
  }
}

export const launchContext = typeof window === 'undefined'
  ? { getSnapshot: () => emptySnapshot, subscribe: () => () => undefined }
  : createLaunchContextStore()
