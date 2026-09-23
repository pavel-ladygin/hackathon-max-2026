import type { ButtonHTMLAttributes, HTMLAttributes, ImgHTMLAttributes, ReactNode } from "react";
import styles from "./ui.module.css";

type Tone = "primary" | "secondary" | "ghost";
export type ButtonState = "idle" | "loading" | "success";

export function Button({ tone = "primary", state = "idle", loadingLabel = "Загружаем…", successLabel = "Готово", className = "", children, disabled, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: Tone; state?: ButtonState; loadingLabel?: ReactNode; successLabel?: ReactNode }) {
  const content = state === "loading" ? <><span className={styles.buttonIcon} aria-hidden="true"><span className={styles.buttonSpinner} /></span>{loadingLabel}</> : state === "success" ? <><span className={styles.buttonIcon} aria-hidden="true"><span className={styles.buttonCheck}>✓</span></span>{successLabel}</> : children;
  return <button className={`${styles.button} ${styles[tone]} ${styles[`buttonState_${state}`]} ${className}`} aria-busy={state === "loading" ? true : undefined} disabled={disabled || state !== "idle"} {...props}>{content}</button>;
}

export function HeartIcon({ filled = false }: { filled?: boolean }) {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M20.8 8.8c0 5.5-8.8 10.2-8.8 10.2S3.2 14.3 3.2 8.8A4.7 4.7 0 0 1 12 6.2a4.7 4.7 0 0 1 8.8 2.6Z" fill={filled ? "currentColor" : "none"} stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" /></svg>;
}

export function ArrowLeftIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m14.5 5-7 7 7 7M8 12h12" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg>;
}

type IconButtonVariant = "default" | "surface" | "favorite";
export function IconButton({ label, children, filled = false, selected = false, variant = "default", className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; filled?: boolean; selected?: boolean; variant?: IconButtonVariant }) {
  return <button type="button" aria-label={label} aria-pressed={variant === "favorite" || selected ? selected : undefined} title={label} className={`${styles.iconButton} ${styles[`iconButton_${variant}`]} ${filled ? styles.filled : ""} ${selected ? styles.iconButtonSelected : ""} ${className}`} {...props}>{children}</button>;
}

export type FavoriteButtonSize = "card" | "action";
export function FavoriteButton({ selected = false, pending = false, label, size = "card", onToggle, className = "", ...props }: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "onClick"> & { selected?: boolean; pending?: boolean; label: string; size?: FavoriteButtonSize; onToggle?: () => void }) {
  return <button {...props} type="button" className={`${styles.favoriteButton} ${styles[`favoriteButton_${size}`]} ${selected ? styles.favoriteButtonSelected : ""} ${pending ? styles.favoriteButtonPending : ""} ${className}`} aria-label={label} aria-pressed={selected} aria-busy={pending ? true : undefined} disabled={pending || props.disabled} onClick={onToggle}><span className={styles.favoriteIcon} aria-hidden="true"><HeartIcon filled={selected} /></span></button>;
}

export function Chip({ selected = false, children, className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { selected?: boolean }) {
  return <button type="button" aria-pressed={selected} className={`${styles.chip} ${selected ? styles.chipSelected : ""} ${className}`} {...props}>{children}</button>;
}

export function ChipGroup({ children, label, className = "" }: { children: ReactNode; label?: string; className?: string }) {
  return <div className={`${styles.chipGroup} ${className}`} role={label ? "group" : undefined} aria-label={label}>{children}</div>;
}

export function EventImage({ alt, className = "", width = 400, height = 400, ...props }: ImgHTMLAttributes<HTMLImageElement>) {
  return <img alt={alt} className={`${styles.eventImage} ${className}`} width={width} height={height} loading="lazy" {...props} />;
}

export type EventCardData = { id?: string; title: string; meta?: string; eyebrow?: string; image: string };
export function EventCard({ event, compact = false, onClick, className = "" }: { event: EventCardData; compact?: boolean; onClick?: () => void; className?: string }) {
  const content = <><EventImage src={event.image} alt="" /><span className={styles.eventCopy}><span className={styles.eyebrow}>{event.eyebrow ?? "СОБЫТИЕ"}</span><strong>{event.title}</strong>{event.meta && <span>{event.meta}</span>}</span><span className={styles.arrow} aria-hidden="true">›</span></>;
  return onClick ? <button type="button" className={`${styles.eventCard} ${compact ? styles.compact : ""} ${className}`} onClick={onClick}>{content}</button> : <article className={`${styles.eventCard} ${compact ? styles.compact : ""} ${className}`}>{content}</article>;
}

export function PageShell({ children, className = "", withBottomNav = false }: { children: ReactNode; className?: string; withBottomNav?: boolean }) {
  return <main className={`${styles.pageShell} ${withBottomNav ? styles.pageShellWithNav : ""} ${className}`}>{children}</main>;
}

export function PageContent({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`${styles.pageContent} ${className}`}>{children}</div>;
}

export function TopBar({ title, onBack, right, prominentBack = false }: { title?: ReactNode; onBack?: () => void; right?: ReactNode; prominentBack?: boolean }) {
  return <header className={styles.topBar}><div className={styles.topBarSide}>{onBack && <IconButton label="Назад" variant={prominentBack ? "surface" : "default"} className={prominentBack ? styles.prominentBack : ""} onClick={onBack}><ArrowLeftIcon /></IconButton>}</div><div className={styles.topBarTitle}>{title}</div><div className={styles.topBarSide}>{right}</div></header>;
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

export type NavIconName = "home" | "calendar" | "saved";
export type NavItem = { id: string; label: string; icon: NavIconName };
function NavIcon({ name }: { name: NavIconName }) {
  if (name === "home") return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m4 10 8-6 8 6v9a1 1 0 0 1-1 1h-4v-6H9v6H5a1 1 0 0 1-1-1Z" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" /></svg>;
  if (name === "calendar") return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><rect x="4" y="5.5" width="16" height="14" rx="2" fill="none" stroke="currentColor" strokeWidth="1.8" /><path d="M8 3.5v4M16 3.5v4M4 10h16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg>;
  return <HeartIcon />;
}
export function BottomNav({ items, activeId, onChange }: { items: NavItem[]; activeId?: string; onChange?: (id: string) => void }) {
  return <nav className={styles.bottomNav} aria-label="Основная навигация">{items.map(item => <button type="button" key={item.id} className={`${styles.navItem} ${activeId === item.id ? styles.active : ""}`} aria-current={activeId === item.id ? "page" : undefined} onClick={() => onChange?.(item.id)}><span className={`${styles.navIcon} ${styles[`navIcon_${item.icon}`]}`}><NavIcon name={item.icon} /></span><span>{item.label}</span></button>)}</nav>;
}
