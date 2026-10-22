import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react'
import { createPortal } from 'react-dom'
import { CITIES, searchCities } from '../lib/cities'
import styles from './city-picker.module.css'

export type CityPickerProps = {
  cityId: string | null
  onSelect: (id: string) => Promise<void>
  pending?: boolean
  error?: unknown
  onClose: () => void
}

export function CityPicker({ cityId, onSelect, pending = false, error, onClose }: CityPickerProps) {
  const [query, setQuery] = useState('')
  const [savingId, setSavingId] = useState<string | null>(null)
  const [activeOption, setActiveOption] = useState({ query: '', index: 0 })
  const dialogRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const previousFocus = useRef<HTMLElement | null>(null)
  const onCloseRef = useRef(onClose)
  const pendingRef = useRef(pending)
  const cities = useMemo(() => searchCities(query), [query])
  const firstReadyIndex = Math.max(0, cities.findIndex((city) => city.id === cityId && city.ready))
  const activeIndex = activeOption.query === query ? activeOption.index : firstReadyIndex

  useEffect(() => { onCloseRef.current = onClose }, [onClose])
  useEffect(() => { pendingRef.current = pending }, [pending])

  useEffect(() => {
    previousFocus.current = document.activeElement instanceof HTMLElement && document.activeElement !== document.body
      ? document.activeElement
      : null
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    searchRef.current?.focus()

    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape' && !pendingRef.current) {
        event.preventDefault()
        onCloseRef.current()
        return
      }
      if (event.key !== 'Tab') return
      const controls = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled):not([tabindex="-1"]), input:not(:disabled)') ?? [])
      if (!controls.length) {
        event.preventDefault()
        dialogRef.current?.focus()
        return
      }
      const current = controls.indexOf(document.activeElement as HTMLElement)
      if (event.shiftKey && current <= 0) {
        event.preventDefault()
        controls.at(-1)?.focus()
      } else if (!event.shiftKey && (current === controls.length - 1 || current < 0)) {
        event.preventDefault()
        controls[0]?.focus()
      }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      document.removeEventListener('keydown', onKeyDown)
      window.setTimeout(() => previousFocus.current?.focus(), 0)
    }
  }, [])

  const closeOnBackdrop = (event: MouseEvent<HTMLDivElement>) => {
    if (event.target === event.currentTarget && !pending) onClose()
  }

  const choose = async (id: string) => {
    if (pending || savingId || id === cityId || !CITIES.some((city) => city.id === id && city.ready)) return
    setSavingId(id)
    try {
      await onSelect(id)
      onClose()
    } catch {
      // The parent exposes the mutation error; keep the picker open for retry.
    } finally {
      setSavingId(null)
    }
  }

  const focusOption = (index: number) => {
    const option = cities[index]
    if (!option?.ready) return
    setActiveOption({ query, index })
    document.getElementById(`city-picker-option-${index}`)?.focus()
  }

  const handleSearchKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== 'ArrowDown' || !cities.length) return
    event.preventDefault()
    const next = cities.findIndex((city, index) => index >= activeIndex && city.ready)
    focusOption(next >= 0 ? next : cities.findIndex((city) => city.ready))
  }

  const handleListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!cities.length) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const direction = event.key === 'ArrowDown' ? 1 : -1
      let index = activeIndex
      for (let tries = 0; tries < cities.length; tries += 1) {
        index = (index + direction + cities.length) % cities.length
        if (cities[index]?.ready) { focusOption(index); return }
      }
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      const readyIndices = cities.map((city, index) => city.ready ? index : -1).filter((index) => index >= 0)
      if (readyIndices.length) focusOption(event.key === 'Home' ? readyIndices[0] : readyIndices.at(-1)!)
    } else if ((event.key === 'Enter' || event.key === ' ') && cities[activeIndex]?.ready) {
      event.preventDefault()
      void choose(cities[activeIndex].id)
    }
  }

  return createPortal(
    <div className={styles.backdrop} onMouseDown={closeOnBackdrop}>
      <div ref={dialogRef} className={styles.sheet} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="city-picker-title">
        <div className={styles.handle} aria-hidden="true" />
        <header className={styles.header}>
          <div><span className={styles.eyebrow}>АФИША ПО ГОРОДУ</span><h2 id="city-picker-title">Выберите город</h2></div>
          <button className={styles.close} type="button" aria-label="Закрыть выбор города" disabled={pending} onClick={onClose}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>
        <label className={styles.searchLabel} htmlFor="city-picker-search">Найти город</label>
        <div className={styles.searchBox}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="10.8" cy="10.8" r="6.8" /><path d="m16 16 4.5 4.5" /></svg>
          <input ref={searchRef} id="city-picker-search" type="search" autoComplete="off" value={query} disabled={pending} onChange={(event) => setQuery(event.target.value)} onKeyDown={handleSearchKeyDown} placeholder="Например, Санкт-Петербург" />
          {query ? <button type="button" aria-label="Очистить поиск" disabled={pending} onClick={() => { setQuery(''); searchRef.current?.focus() }}>×</button> : null}
        </div>
        {error ? <p className={styles.error} role="alert">Не удалось сохранить город. Попробуйте выбрать его ещё раз.</p> : null}
        <div className={styles.cityList} role="listbox" aria-label="Города" aria-busy={pending} onKeyDown={handleListKeyDown}>
          {cities.map((city) => {
            const selected = city.id === cityId
            const saving = city.id === savingId && pending
            return <button
              id={`city-picker-option-${cities.indexOf(city)}`}
              key={city.id}
              type="button"
              role="option"
              tabIndex={city.ready && cities.indexOf(city) === activeIndex ? 0 : -1}
              aria-selected={selected}
              aria-disabled={!city.ready || undefined}
              disabled={!city.ready || pending}
              className={`${styles.city} ${selected ? styles.selected : ''} ${!city.ready ? styles.unavailable : ''}`}
              onFocus={() => setActiveOption({ query, index: cities.indexOf(city) })}
              onClick={() => void choose(city.id)}
            >
              <span className={styles.cityCopy}><strong>{city.name}</strong>{city.ready ? <small>{city.id === cityId ? 'Текущий город' : 'События и места рядом'}</small> : <small>Скоро</small>}</span>
              {saving ? <span className={styles.spinner} role="status" aria-label="Сохраняем город" /> : selected ? <span className={styles.check} aria-label="Выбран">✓</span> : !city.ready ? <span className={styles.soon}>Скоро</span> : <span className={styles.arrow} aria-hidden="true">›</span>}
            </button>
          })}
          {cities.length === 0 ? <p className={styles.empty} role="status">Не нашли такой город. Проверьте запрос.</p> : null}
        </div>
        {pending ? <p className={styles.saving} role="status">Сохраняем выбор…</p> : null}
      </div>
    </div>,
    document.body,
  )
}
