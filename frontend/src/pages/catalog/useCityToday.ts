import { useEffect, useState } from 'react'

export function cityDate(timezone: string, timestamp = Date.now()): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(timestamp)
}

export function useCityToday(timezone: string): string {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const refresh = () => setNow(Date.now())
    let timer: ReturnType<typeof setTimeout>
    // City timezones use whole minutes. Align the refresh with midnight even
    // when the catalog was opened in the middle of a minute.
    const schedule = () => {
      timer = setTimeout(() => { refresh(); schedule() }, 60_000 - Date.now() % 60_000)
    }
    schedule()
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => {
      clearTimeout(timer)
      window.removeEventListener('focus', refresh)
      document.removeEventListener('visibilitychange', refresh)
    }
  }, [])

  return cityDate(timezone, now)
}
