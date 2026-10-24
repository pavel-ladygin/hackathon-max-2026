import { AnimatePresence, motion, useIsPresent, useReducedMotion } from 'motion/react'
import { DatePicker } from './date-picker'
import styles from './room-date-choices.module.css'

const dateLabel = new Intl.DateTimeFormat('ru-RU', { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'UTC' })

function compactDateLabel(value: string) {
  const date = new Date(`${value}T12:00:00Z`)
  if (Number.isNaN(date.getTime())) return value
  const label = dateLabel.format(date)
  return label.charAt(0).toUpperCase() + label.slice(1)
}

function SelectedDateChip({ value, selected, onChange, disabled, reducedMotion }: { value: string; selected: string[]; onChange: (dates: string[]) => void; disabled: boolean; reducedMotion: boolean }) {
  const isPresent = useIsPresent()
  const label = compactDateLabel(value)
  const unavailable = disabled || !isPresent
  return <motion.button
    type="button"
    className={styles.chip}
    aria-label={`Удалить ${label}`}
    disabled={unavailable}
    layout={!reducedMotion}
    initial={reducedMotion ? false : { opacity: 0, scale: .92 }}
    animate={{ opacity: 1, scale: 1 }}
    exit={reducedMotion ? undefined : { opacity: 0, scale: .92, transition: { duration: .16 } }}
    transition={{ duration: reducedMotion ? 0 : .18 }}
    onClick={() => { if (isPresent && !disabled) onChange(selected.filter(date => date !== value)) }}
  >
    <span>{label}</span><span aria-hidden="true" className={styles.remove}>×</span>
  </motion.button>
}

export type RoomDateChoicesProps = {
  id: string
  selected: string[]
  onChange: (dates: string[]) => void
  min?: string
  error?: string
  description?: string
  disabled?: boolean
}

export function RoomDateChoices({ id, selected, onChange, min, error, description, disabled = false }: RoomDateChoicesProps) {
  const reducedMotion = useReducedMotion()

  return <div className={styles.root}>
    <DatePicker id={id} className={styles.picker} label="Выбор дат" mode="multiple" value={selected} maxSelections={14} min={min} disabled={disabled} invalid={Boolean(error)} describedBy={error ? `${id}-error` : description ? `${id}-help` : undefined} onChange={onChange} />
    <div className={styles.chips} aria-label="Выбранные даты">
      <AnimatePresence initial={false}>
        {selected.map(value => <SelectedDateChip key={value} value={value} selected={selected} onChange={onChange} disabled={disabled} reducedMotion={Boolean(reducedMotion)} />)}
      </AnimatePresence>
    </div>
    {description ? <span id={`${id}-help`} className={styles.hint}>{description}</span> : null}
    {error ? <span id={`${id}-error`} className={styles.error} role="alert">{error}</span> : null}
  </div>
}
