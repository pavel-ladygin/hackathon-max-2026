import { useEffect, useId, useRef, useState, type KeyboardEvent, type MouseEvent } from "react";
import { createPortal } from "react-dom";
import styles from "./DatePicker.module.css";

const WEEKDAYS = ["Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"];
const MONTH_FORMATTER = new Intl.DateTimeFormat("ru-RU", { month: "long", year: "numeric" });
const DATE_FORMATTER = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long", year: "numeric" });

export type DatePickerProps = {
  id?: string;
  label: string;
  value?: string | null;
  onChange: (value: string | null) => void;
  min?: string;
  max?: string;
  disabled?: boolean;
  className?: string;
};

function parseDate(value: string | null | undefined): Date | null {
  if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return null;
  const [year, month, day] = value.split("-").map(Number);
  const date = new Date(year, month - 1, day);
  return date.getFullYear() === year && date.getMonth() === month - 1 && date.getDate() === day ? date : null;
}

function toValue(date: Date): string {
  return [date.getFullYear(), String(date.getMonth() + 1).padStart(2, "0"), String(date.getDate()).padStart(2, "0")].join("-");
}

function dateOnly(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

function atMonth(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), 1);
}

function clampToBounds(date: Date, min: Date | null, max: Date | null): Date {
  const value = dateOnly(date).getTime();
  if (min && value < min.getTime()) return min;
  if (max && value > max.getTime()) return max;
  return dateOnly(date);
}

function monthDays(month: Date): Date[] {
  const firstDay = (month.getDay() + 6) % 7;
  const first = new Date(month.getFullYear(), month.getMonth(), 1 - firstDay);
  return Array.from({ length: 42 }, (_, index) => new Date(first.getFullYear(), first.getMonth(), first.getDate() + index));
}

function isSameDay(left: Date, right: Date | null): boolean {
  return Boolean(right && left.getTime() === right.getTime());
}

function dateLabel(date: Date): string {
  return DATE_FORMATTER.format(date);
}

export function DatePicker({ id, label, value = null, onChange, min, max, disabled = false, className = "" }: DatePickerProps) {
  const generatedId = useId();
  const fieldId = id ?? `date-picker-${generatedId.replace(/:/g, "")}`;
  const labelId = `${fieldId}-label`;
  const dialogId = `${fieldId}-dialog`;
  const minDate = parseDate(min);
  const maxDate = parseDate(max);
  const selectedDate = parseDate(value);
  const initialDate = clampToBounds(selectedDate ?? new Date(), minDate, maxDate);
  const [open, setOpen] = useState(false);
  const [month, setMonth] = useState(() => atMonth(initialDate));
  const [draft, setDraft] = useState<Date | null>(selectedDate);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);

  const close = () => {
    setOpen(false);
    window.setTimeout(() => (previousFocusRef.current ?? triggerRef.current)?.focus(), 0);
  };

  const openPicker = () => {
    previousFocusRef.current = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null;
    const nextDate = clampToBounds(selectedDate ?? new Date(), minDate, maxDate);
    setDraft(selectedDate);
    setMonth(atMonth(nextDate));
    setOpen(true);
  };

  useEffect(() => {
    if (!open) return;
    const dialog = dialogRef.current;
    const firstFocusable = dialog?.querySelector<HTMLElement>("button:not([disabled]), [tabindex=\"0\"]");
    firstFocusable?.focus();
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close();
        return;
      }
      if (event.key !== "Tab" || !dialog) return;
      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>("button:not([disabled]), [tabindex=\"0\"]"));
      if (focusable.length === 0) return;
      const currentIndex = focusable.indexOf(document.activeElement as HTMLElement);
      const nextIndex = event.shiftKey
        ? (currentIndex <= 0 ? focusable.length - 1 : currentIndex - 1)
        : (currentIndex === focusable.length - 1 ? 0 : currentIndex + 1);
      event.preventDefault();
      focusable[nextIndex]?.focus();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => { document.body.style.overflow = previousOverflow; };
  }, [open]);

  const handleBackdropClick = (event: MouseEvent<HTMLDivElement>) => {
    if (event.target === event.currentTarget) close();
  };

  const handleDayKeyDown = (event: KeyboardEvent<HTMLButtonElement>, date: Date) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setDraft(date);
    }
  };

  const isDisabledDate = (date: Date) => Boolean((minDate && date < minDate) || (maxDate && date > maxDate));
  const days = monthDays(month);
  const display = selectedDate ? `${String(selectedDate.getDate()).padStart(2, "0")}.${String(selectedDate.getMonth() + 1).padStart(2, "0")}.${selectedDate.getFullYear()}` : "ДД.ММ.ГГГГ";

  return (
    <div className={`${styles.root} ${className}`}>
      <span id={labelId} className={styles.label}>{label}</span>
      <button
        ref={triggerRef}
        id={fieldId}
        type="button"
        className={`${styles.trigger} ${selectedDate ? styles.triggerFilled : ""}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-labelledby={labelId}
        disabled={disabled}
        onClick={openPicker}
      >
        <span>{display}</span>
        <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><rect x="4" y="5.5" width="16" height="14" rx="2" /><path d="M8 3.5v4M16 3.5v4M4 10h16" /></svg>
      </button>

      {open && createPortal(<div className={styles.backdrop} onMouseDown={handleBackdropClick}>
        <div ref={dialogRef} id={dialogId} className={styles.sheet} role="dialog" aria-modal="true" aria-label="Выбор даты">
          <div className={styles.handle} aria-hidden="true" />
          <div className={styles.sheetHeader}>
            <div>
              <span className={styles.sheetEyebrow}>{label}</span>
              <strong>{draft ? dateLabel(draft) : "Выберите дату"}</strong>
            </div>
            <button type="button" className={styles.close} aria-label="Закрыть календарь" onClick={close}>×</button>
          </div>
          <div className={styles.monthNav}>
            <button type="button" className={styles.navButton} aria-label="Предыдущий месяц" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}>‹</button>
            <strong>{MONTH_FORMATTER.format(month)}</strong>
            <button type="button" className={styles.navButton} aria-label="Следующий месяц" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}>›</button>
          </div>
          <div className={styles.weekdays} aria-hidden="true">{WEEKDAYS.map(day => <span key={day}>{day}</span>)}</div>
          <div className={styles.grid} role="grid" aria-label={MONTH_FORMATTER.format(month)}>
            {days.map(date => {
              const unavailable = isDisabledDate(date);
              const outside = date.getMonth() !== month.getMonth();
              return <button
                key={toValue(date)}
                type="button"
                role="gridcell"
                className={`${styles.day} ${outside ? styles.outside : ""} ${isSameDay(date, draft) ? styles.selected : ""}`}
                aria-label={dateLabel(date)}
                aria-selected={isSameDay(date, draft)}
                disabled={unavailable}
                onClick={() => setDraft(date)}
                onKeyDown={event => handleDayKeyDown(event, date)}
              >{date.getDate()}</button>;
            })}
          </div>
          <div className={styles.actions}>
            <button type="button" className={styles.clear} onClick={() => { setDraft(null); onChange(null); close(); }}>Очистить</button>
            <button type="button" className={styles.apply} onClick={() => { onChange(draft ? toValue(draft) : null); close(); }}>Применить</button>
          </div>
        </div>
      </div>, document.body)}
    </div>
  );
}
