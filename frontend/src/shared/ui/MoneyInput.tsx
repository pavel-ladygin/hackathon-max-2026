import styles from './money-input.module.css'

export function MoneyInput({ id, label, value, onChange, onCommit, min = 0, max = 1_000_000, invalid, describedBy, required = false, suffix }: { id: string; label?: string; value: string; onChange: (value: string) => void; onCommit?: (value: string) => void; min?: number; max?: number; invalid?: boolean; describedBy?: string; required?: boolean; suffix?: string }) {
  return <span className={styles.root}>{label ? <label htmlFor={id}>{label}</label> : null}<span className={`${styles.control} ${suffix ? styles.withSuffix : ''}`}><input id={id} type="text" inputMode="decimal" autoComplete="off" value={value} required={required} aria-invalid={invalid || undefined} aria-describedby={describedBy} onChange={(event) => onChange(event.target.value)} onBlur={() => onCommit?.(value)} data-min={min} data-max={max} />{suffix ? <span className={styles.suffix} aria-hidden="true">{suffix}</span> : null}</span></span>
}
