import type { ButtonHTMLAttributes, HTMLAttributes, ImgHTMLAttributes, ReactNode } from "react";
import styles from "./ui.module.css";

type Tone = "primary" | "secondary" | "ghost";

export function Button({ tone = "primary", className = "", children, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: Tone }) {
  return <button className={`${styles.button} ${styles[tone]} ${className}`} {...props}>{children}</button>;
}

export function IconButton({ label, children, filled = false, className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; filled?: boolean }) {
  return <button type="button" aria-label={label} title={label} className={`${styles.iconButton} ${filled ? styles.filled : ""} ${className}`} {...props}>{children}</button>;
}

export function Chip({ selected = false, children, className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { selected?: boolean }) {
  return <button type="button" aria-pressed={selected} className={`${styles.chip} ${selected ? styles.chipSelected : ""} ${className}`} {...props}>{children}</button>;
}

export function ChipGroup({ children, label, className = "" }: { children: ReactNode; label?: string; className?: string }) {
  return <div className={`${styles.chipGroup} ${className}`} role={label ? "group" : undefined} aria-label={label}>{children}</div>;
}

export function EventImage({ alt, className = "", ...props }: ImgHTMLAttributes<HTMLImageElement>) {
  return <img alt={alt} className={`${styles.eventImage} ${className}`} loading="lazy" {...props} />;
}

export type EventCardData = { id?: string; title: string; meta?: string; eyebrow?: string; image: string };
export function EventCard({ event, compact = false, onClick, className = "" }: { event: EventCardData; compact?: boolean; onClick?: () => void; className?: string }) {
  const content = <><EventImage src={event.image} alt={event.title} /><span className={styles.eventCopy}><span className={styles.eyebrow}>{event.eyebrow ?? "СОБЫТИЕ"}</span><strong>{event.title}</strong>{event.meta && <span>{event.meta}</span>}</span><span className={styles.arrow} aria-hidden="true">›</span></>;
  return onClick ? <button type="button" className={`${styles.eventCard} ${compact ? styles.compact : ""} ${className}`} onClick={onClick}>{content}</button> : <article className={`${styles.eventCard} ${compact ? styles.compact : ""} ${className}`}>{content}</article>;
}

export function PageShell({ children, className = "", withBottomNav = false }: { children: ReactNode; className?: string; withBottomNav?: boolean }) {
  return <main className={`${styles.pageShell} ${withBottomNav ? styles.pageShellWithNav : ""} ${className}`}>{children}</main>;
}

export function PageContent({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`${styles.pageContent} ${className}`}>{children}</div>;
}

export function TopBar({ title, onBack, right }: { title?: ReactNode; onBack?: () => void; right?: ReactNode }) {
  return <header className={styles.topBar}><div className={styles.topBarSide}>{onBack && <IconButton label="Назад" onClick={onBack}>‹</IconButton>}</div><div className={styles.topBarTitle}>{title}</div><div className={styles.topBarSide}>{right}</div></header>;
}

export function PrivacyNote({ title = "Ваши ответы видны только вам", children = "Мы показываем участникам только общие совпадения — никакой неловкости." }: { title?: string; children?: ReactNode }) {
  return <aside className={styles.privacy}><span aria-hidden="true">♢</span><div><strong>{title}</strong><div>{children}</div></div></aside>;
}

export function Loading({ label = "Загружаем…", inline = false }: { label?: string; inline?: boolean }) {
  return <div className={`${styles.status} ${inline ? styles.statusInline : ""}`} role="status" aria-live="polite"><span className={styles.spinner} aria-hidden="true" /><p>{label}</p></div>;
}

export function Empty({ title = "Здесь пока пусто", description, action, inline = false }: { title?: string; description?: string; action?: ReactNode; inline?: boolean }) {
  return <div className={`${styles.status} ${inline ? styles.statusInline : ""}`}><span className={styles.statusIcon} aria-hidden="true">✦</span><h3>{title}</h3>{description && <p>{description}</p>}{action}</div>;
}

export function ErrorState({ title = "Что-то пошло не так", description, action, inline = false }: { title?: string; description?: string; action?: ReactNode; inline?: boolean }) {
  return <div className={`${styles.status} ${inline ? styles.statusInline : ""}`} role="alert"><span className={styles.statusIcon} aria-hidden="true">!</span><h3>{title}</h3>{description && <p>{description}</p>}{action}</div>;
}

export function InlineNotice({ tone = "neutral", children, className = "", ...props }: HTMLAttributes<HTMLDivElement> & { tone?: "neutral" | "danger" | "success" }) {
  return <div className={`${styles.notice} ${styles[`notice_${tone}`]} ${className}`} role={tone === "danger" ? "alert" : "status"} {...props}>{children}</div>;
}

export function FieldError({ id, children }: { id: string; children?: ReactNode }) {
  if (!children) return null;
  return <span id={id} className={styles.fieldError} role="alert">{children}</span>;
}

export function Skeleton({ className = "", label = "Загрузка" }: { className?: string; label?: string }) {
  return <span className={`${styles.skeleton} ${className}`} role="status" aria-label={label} />;
}

export type NavItem = { id: string; label: string; icon: ReactNode };
export function BottomNav({ items, activeId, onChange }: { items: NavItem[]; activeId?: string; onChange?: (id: string) => void }) {
  return <nav className={styles.bottomNav} aria-label="Основная навигация">{items.map(item => <button type="button" key={item.id} className={`${styles.navItem} ${activeId === item.id ? styles.active : ""}`} aria-current={activeId === item.id ? "page" : undefined} onClick={() => onChange?.(item.id)}>{item.icon}<span>{item.label}</span></button>)}</nav>;
}
