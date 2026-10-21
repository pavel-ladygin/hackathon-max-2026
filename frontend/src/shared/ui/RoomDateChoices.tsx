import { DatePicker } from './date-picker'
import { ChoiceChips } from './ChoiceChips'
import styles from './room-date-choices.module.css'

const dateLabel = new Intl.DateTimeFormat('ru-RU', { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'UTC' })

function compactDateLabel(value: string, fallback: string) {
  const date = new Date(`${value}T12:00:00Z`)
  if (Number.isNaN(date.getTime())) return fallback
  const label = dateLabel.format(date)
  return label.charAt(0).toUpperCase() + label.slice(1)
}

export function RoomDateChoices({ id, options, selected, onToggle, onLimitReached, date, onDateChange, onAddDate, min, error, description, disabled = false, buttonLabel = 'Добавить дату' }: { id: string; options: Array<[string, string]>; selected: string[]; onToggle: (value: string) => void; onLimitReached?: () => void; date: string; onDateChange: (value: string) => void; onAddDate: () => void; min?: string; error?: string; description?: string; disabled?: boolean; buttonLabel?: string }) {
  const toggleDate = (value: string) => { if (selected.includes(value) || selected.length < 14) onToggle(value); else onLimitReached?.() }
  return <div className={styles.root}><ChoiceChips options={options} selected={selected} onToggle={toggleDate} formatLabel={compactDateLabel} className={styles.choices} /><div className={styles.add}><DatePicker id={id} label="Другая дата" value={date} min={min} disabled={disabled} invalid={Boolean(error)} describedBy={error ? `${id}-error` : description ? `${id}-help` : undefined} onChange={(value) => onDateChange(value ?? '')} /><button type="button" disabled={disabled || !date || selected.length >= 14} onClick={onAddDate}>{buttonLabel}</button></div>{description ? <span id={`${id}-help`} className={styles.hint}>{description}</span> : null}{error ? <span id={`${id}-error`} className={styles.error} role="alert">{error}</span> : null}</div>
}
