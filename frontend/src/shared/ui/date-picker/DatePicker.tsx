import { useEffect, useId, useRef, useState, type KeyboardEvent, type MouseEvent } from "react";
import { createPortal } from "react-dom";
import styles from "./DatePicker.module.css";

const WEEKDAYS = ["Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"];
const MONTH_FORMATTER = new Intl.DateTimeFormat("ru-RU", { month: "long", year: "numeric" });
const DATE_FORMATTER = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long", year: "numeric" });
const SHORT_DATE_FORMATTER = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "long" });

type SharedProps = {
  id?: string;
  label: string;
  min?: string;
  max?: string;
  disabled?: boolean;
  className?: string;
  invalid?: boolean;
  describedBy?: string;
};

export type DatePickerSingleProps = SharedProps & {
  mode?: "single";
  value?: string | null;
  onChange: (value: string | null) => void;
  maxSelections?: never;
};

export type DatePickerMultipleProps = SharedProps & {
  mode: "multiple";
  value: string[];
  onChange: (values: string[]) => void;
  maxSelections?: number;
};

export type DatePickerProps = DatePickerSingleProps | DatePickerMultipleProps;

function parseDate(value: string | null | undefined): Date | null {
  if (!value || !/^\d{4}-\d{2}-\d{2}$/.test(value)) return null;
  const [year, month, day] = value.split("-").map(Number);
  const date = new Date(year, month - 1, day);
  return date.getFullYear() === year && date.getMonth() === month - 1 && date.getDate() === day ? date : null;
}

function toValue(date: Date): string {
  return [date.getFullYear(), String(date.getMonth() + 1).padStart(2, "0"), String(date.getDate()).padStart(2, "0")].join("-");
}

function dateOnly(date: Date): Date { return new Date(date.getFullYear(), date.getMonth(), date.getDate()); }
function atMonth(date: Date): Date { return new Date(date.getFullYear(), date.getMonth(), 1); }

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

function dateLabel(date: Date): string { return DATE_FORMATTER.format(date); }
function uniqueValidDates(values: string[]): string[] {
  return [...new Set(values)].filter(value => parseDate(value)).sort();
}

export function DatePicker(props: DatePickerProps) {
  const { id, label, min, max, disabled = false, className = "", invalid = false, describedBy } = props;
  const multiple = props.mode === "multiple";
  const singleValue = !multiple ? (props.value ?? null) : null;
  const selectedDate = parseDate(singleValue);
  const selectedValues = multiple ? uniqueValidDates(props.value) : [];
  const selectedDates = multiple ? selectedValues.map(value => parseDate(value)!).filter(Boolean) : [];
  const generatedId = useId();
  const fieldId = id ?? `date-picker-${generatedId.replace(/:/g, "")}`;
  const labelId = `${fieldId}-label`;
  const dialogId = `${fieldId}-dialog`;
  const minDate = parseDate(min);
  const maxDate = parseDate(max);
  const initialDate = clampToBounds(selectedDates[0] ?? selectedDate ?? new Date(), minDate, maxDate);
  const [open, setOpen] = useState(false);
  const [month, setMonth] = useState(() => atMonth(initialDate));
  const [draftSingle, setDraftSingle] = useState<Date | null>(selectedDate);
  const [draftMultiple, setDraftMultiple] = useState<string[]>(selectedValues);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const draftValues = multiple ? draftMultiple : [];
  const maxSelections = multiple ? props.maxSelections : undefined;

  const close = () => {
    setOpen(false);
    window.setTimeout(() => (previousFocusRef.current ?? triggerRef.current)?.focus(), 0);
  };

  const openPicker = () => {
    previousFocusRef.current = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null;
    const nextDate = clampToBounds(selectedDates[0] ?? selectedDate ?? new Date(), minDate, maxDate);
    setDraftSingle(selectedDate);
    setDraftMultiple(selectedValues);
    setMonth(atMonth(nextDate));
    setOpen(true);
  };

  useEffect(() => {
    if (!open) return;
    const dialog = dialogRef.current;
    const firstFocusable = dialog?.querySelector<HTMLElement>("button:not([disabled]), [tabindex=\"0\"]");
    firstFocusable?.focus();
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") { event.preventDefault(); close(); return; }
      if (event.key !== "Tab" || !dialog) return;
      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>("button:not([disabled]), [tabindex=\"0\"]"));
      if (focusable.length === 0) return;
      const currentIndex = focusable.indexOf(document.activeElement as HTMLElement);
      const nextIndex = event.shiftKey ? (currentIndex <= 0 ? focusable.length - 1 : currentIndex - 1) : (currentIndex === focusable.length - 1 ? 0 : currentIndex + 1);
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

  const handleBackdropClick = (event: MouseEvent<HTMLDivElement>) => { if (event.target === event.currentTarget) close(); };
  const chooseDate = (date: Date) => {
    if (!multiple) { setDraftSingle(date); return; }
    const value = toValue(date);
    if (draftValues.includes(value)) {
      setDraftMultiple(draftValues.filter(item => item !== value));
    } else if (maxSelections === undefined || draftValues.length < maxSelections) {
      setDraftMultiple([...draftValues, value].sort());
    }
  };

  const handleDayKeyDown = (event: KeyboardEvent<HTMLButtonElement>, date: Date) => {
    if (event.key === "Enter" || event.key === " ") { event.preventDefault(); chooseDate(date); return; }
    const next = new Date(date);
    if (event.key === "ArrowLeft") next.setDate(next.getDate() - 1);
    else if (event.key === "ArrowRight") next.setDate(next.getDate() + 1);
    else if (event.key === "ArrowUp") next.setDate(next.getDate() - 7);
    else if (event.key === "ArrowDown") next.setDate(next.getDate() + 7);
    else if (event.key === "Home") next.setDate(next.getDate() - ((next.getDay() + 6) % 7));
    else if (event.key === "End") next.setDate(next.getDate() + (6 - ((next.getDay() + 6) % 7)));
    else if (event.key === "PageUp") next.setMonth(next.getMonth() - 1);
    else if (event.key === "PageDown") next.setMonth(next.getMonth() + 1);
    else return;
    event.preventDefault();
    const bounded = clampToBounds(next, minDate, maxDate);
    setMonth(atMonth(bounded));
    window.setTimeout(() => document.getElementById(`${fieldId}-day-${toValue(bounded)}`)?.focus(), 0);
  };

  const isDisabledDate = (date: Date) => Boolean((minDate && date < minDate) || (maxDate && date > maxDate));
  const days = monthDays(month);
  const summary = multiple
    ? selectedDates.length === 0
      ? "Выберите даты"
      : selectedDates.length <= 3
        ? selectedDates.map(date => SHORT_DATE_FORMATTER.format(date)).join(", ").replace(/, ([^,]*)$/, " и $1")
        : `Выбрано дат: ${selectedDates.length}`
    : selectedDate
      ? `${String(selectedDate.getDate()).padStart(2, "0")}.${String(selectedDate.getMonth() + 1).padStart(2, "0")}.${selectedDate.getFullYear()}`
      : "ДД.ММ.ГГГГ";
  const draftSingleLabel = draftSingle ? dateLabel(draftSingle) : "Выберите дату";
  const draftMultipleLabel = draftValues.length ? `Выбрано дат: ${draftValues.length}` : "Выберите даты";

  return (
    <div className={`${styles.root} ${className}`}>
      <span id={labelId} className={styles.label}>{label}</span>
      <button ref={triggerRef} id={fieldId} type="button" className={`${styles.trigger} ${(multiple ? selectedValues.length > 0 : Boolean(selectedDate)) ? styles.triggerFilled : ""}`} aria-haspopup="dialog" aria-expanded={open} aria-labelledby={labelId} aria-invalid={invalid || undefined} aria-describedby={describedBy} disabled={disabled} onClick={openPicker}>
        <span>{summary}</span>
        <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><rect x="4" y="5.5" width="16" height="14" rx="2" /><path d="M8 3.5v4M16 3.5v4M4 10h16" /></svg>
      </button>

      {open && createPortal(<div className={styles.backdrop} onMouseDown={handleBackdropClick}>
        <div ref={dialogRef} id={dialogId} className={styles.sheet} role="dialog" aria-modal="true" aria-label={multiple ? "Выбор дат" : "Выбор даты"}>
          <div className={styles.handle} aria-hidden="true" />
          <div className={styles.sheetHeader}>
            <div><span className={styles.sheetEyebrow}>{label}</span><strong>{multiple ? draftMultipleLabel : draftSingleLabel}</strong></div>
            <button type="button" className={styles.close} aria-label="Закрыть календарь" onClick={close}><svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m6 6 12 12M18 6 6 18" /></svg></button>
          </div>
          <div className={styles.monthNav}>
            <button type="button" className={styles.navButton} aria-label="Предыдущий месяц" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}><svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m15 18-6-6 6-6" /></svg></button>
            <strong>{MONTH_FORMATTER.format(month)}</strong>
            <button type="button" className={styles.navButton} aria-label="Следующий месяц" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}><svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m9 18 6-6-6-6" /></svg></button>
          </div>
          <div className={styles.weekdays} aria-hidden="true">{WEEKDAYS.map(day => <span key={day}>{day}</span>)}</div>
          <div className={styles.grid} role="grid" aria-label={MONTH_FORMATTER.format(month)}>
            {days.map(date => {
              const unavailable = isDisabledDate(date);
              const outside = date.getMonth() !== month.getMonth();
              const value = toValue(date);
              const selected = multiple ? draftValues.includes(value) : Boolean(draftSingle && draftSingle.getTime() === date.getTime());
              const atLimit = multiple && !selected && maxSelections !== undefined && draftValues.length >= maxSelections;
              return <button key={value} id={`${fieldId}-day-${value}`} type="button" role="gridcell" className={`${styles.day} ${outside ? styles.outside : ""} ${selected ? styles.selected : ""}`} aria-label={dateLabel(date)} aria-selected={selected} aria-disabled={atLimit || undefined} disabled={unavailable} onClick={() => chooseDate(date)} onKeyDown={event => handleDayKeyDown(event, date)}>{date.getDate()}</button>;
            })}
          </div>
          {multiple && maxSelections !== undefined ? <p className={styles.selectionCount} aria-live="polite">{draftValues.length} из {maxSelections}</p> : null}
          <div className={styles.actions}>
            <button type="button" className={styles.clear} onClick={() => {
              if (multiple) setDraftMultiple([]);
              else { setDraftSingle(null); props.onChange(null); close(); }
            }}>Очистить</button>
            <button type="button" className={styles.apply} onClick={() => {
              if (multiple) props.onChange([...draftValues]);
              else props.onChange(draftSingle ? toValue(draftSingle) : null);
              close();
            }}>Применить</button>
          </div>
        </div>
      </div>, document.body)}
    </div>
  );
}
