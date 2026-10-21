import { useEffect, useId, useRef, useState } from 'react'
import styles from './custom-select.module.css'

export type SelectOption = { value: string; label: string; disabled?: boolean }
type Props = { id?: string; label: string; value: string; options: SelectOption[]; placeholder?: string; disabled?: boolean; error?: string; description?: string; onChange: (value: string) => void }

export function CustomSelect({ id, label, value, options, placeholder = 'Выберите', disabled = false, error, description, onChange }: Props) {
  const generated = useId().replace(/:/g, '')
  const fieldId = id ?? `select-${generated}`
  const listId = `${fieldId}-options`
  const errorId = error ? `${fieldId}-error` : undefined
  const descriptionId = description ? `${fieldId}-description` : undefined
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(Math.max(0, options.findIndex((item) => item.value === value)))
  const [query, setQuery] = useState('')
  const [menuAbove, setMenuAbove] = useState(false)
  const queryTimer = useRef<number | undefined>(undefined)
  const selected = options.find((item) => item.value === value)
  useEffect(() => {
    if (!open) return
    const close = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('pointerdown', close)
    return () => document.removeEventListener('pointerdown', close)
  }, [open])
  useEffect(() => {
    if (!open) return
    const rect = trigger.current?.getBoundingClientRect()
    if (rect) setMenuAbove(window.innerHeight - rect.bottom < Math.min(280, options.length * 42) && rect.top > window.innerHeight - rect.bottom)
    document.getElementById(`${fieldId}-option-${active}`)?.scrollIntoView?.({ block: 'nearest' })
  }, [active, fieldId, open, options.length])
  const move = (step: number) => {
    if (!options.length || options.every((item) => item.disabled)) return
    let index = active
    for (let attempts = 0; attempts < options.length; attempts += 1) {
      index = (index + step + options.length) % options.length
      if (!options[index]?.disabled) { setActive(index); return }
    }
  }
  const choose = (option: SelectOption) => {
    if (!option || option.disabled) return
    onChange(option.value)
    setOpen(false)
    setQuery('')
    requestAnimationFrame(() => trigger.current?.focus())
  }
  return <div ref={root} className={styles.root}>
    <span className={styles.label} id={`${fieldId}-label`}>{label}</span>
    <button ref={trigger} id={fieldId} type="button" className={styles.trigger} aria-haspopup="listbox" aria-expanded={open} aria-controls={open ? listId : undefined} aria-labelledby={`${fieldId}-label ${fieldId}-value`} aria-invalid={Boolean(error)} aria-describedby={[descriptionId, errorId].filter(Boolean).join(' ') || undefined} disabled={disabled} onClick={() => { setActive(Math.max(0, options.findIndex((item) => item.value === value))); setOpen((current) => !current) }} role="combobox" aria-autocomplete="list" aria-activedescendant={open && options.length ? `${fieldId}-option-${active}` : undefined} onKeyDown={(event) => {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); if (!open) setOpen(true); move(event.key === 'ArrowDown' ? 1 : -1) }
      else if (event.key === 'Home' || event.key === 'End') { event.preventDefault(); const indices = options.map((option, index) => !option.disabled ? index : -1).filter((index) => index >= 0); if (indices.length) setActive(event.key === 'Home' ? indices[0] : indices[indices.length - 1]) }
      else if (event.key === 'Tab' && open) setOpen(false)
      else if (event.key === 'Escape' && open) { event.preventDefault(); setOpen(false); setQuery('') }
      else if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); if (open) choose(options[active]); else setOpen(true) }
      else if (event.key.length === 1 && /\S/.test(event.key)) { window.clearTimeout(queryTimer.current); const next = query + event.key; setQuery(next); queryTimer.current = window.setTimeout(() => setQuery(''), 700); const found = options.findIndex((item) => !item.disabled && item.label.toLocaleLowerCase().startsWith(next.toLocaleLowerCase())); if (found >= 0) { setActive(found); if (!open) setOpen(true) } }
    }}><span id={`${fieldId}-value`} className={selected ? styles.value : styles.placeholder}>{selected?.label ?? placeholder}</span><svg aria-hidden="true" viewBox="0 0 20 20"><path d="m5 7 5 5 5-5" /></svg></button>
    {description ? <span className={styles.description} id={descriptionId}>{description}</span> : null}
    {error ? <span className={styles.error} id={errorId} role="alert">{error}</span> : null}
    {open ? <div id={listId} role="listbox" aria-labelledby={`${fieldId}-label`} className={`${styles.menu} ${menuAbove ? styles.menuAbove : ''}`}>
      {options.map((option, index) => <button id={`${fieldId}-option-${index}`} key={option.value} type="button" tabIndex={-1} role="option" aria-selected={option.value === value} aria-disabled={option.disabled || undefined} disabled={option.disabled} className={`${styles.option} ${active === index ? styles.active : ''}`} onMouseEnter={() => setActive(index)} onClick={() => choose(option)}><span>{option.label}</span>{option.value === value ? <span aria-hidden="true" className={styles.check}>✓</span> : null}</button>)}
    </div> : null}
  </div>
}
