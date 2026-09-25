import { useEffect, useRef, useState, type ButtonHTMLAttributes, type HTMLAttributes, type ImgHTMLAttributes, type ReactNode } from "react";
import styles from "./ui.module.css";

type NavClickToken = { id: string; sequence: number };
let navClickSequence = 0;
let pendingNavClick: NavClickToken | null = null;
let consumedNavClickSequence = 0;

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

type EventImageProps = ImgHTMLAttributes<HTMLImageElement> & { fallbackSrc?: string };
export function EventImage({ alt, className = "", width = 400, height = 400, fallbackSrc = "/events/concert-singer.png", onError, src, srcSet, ...props }: EventImageProps) {
  const [failure, setFailure] = useState<{ key: string; status: "fallback" | "placeholder" } | null>(null);
  const [loadedKey, setLoadedKey] = useState<string | null>(null);
  const [progressive, setProgressive] = useState<{ key: string; phase: "loading" | "full"; src?: string } | null>(null);
  const [previewLoadedKey, setPreviewLoadedKey] = useState<string | null>(null);
  const imageRef = useRef<HTMLImageElement>(null);
  const key = `${src ?? ""}\n${fallbackSrc}`;
  const status = failure?.key === key ? failure.status : "original";
  const activeSrc = status === "fallback" ? fallbackSrc : src;
  const activeKey = `${key}\n${activeSrc ?? ""}`;
  const timepadId = status === "original" ? /^https:\/\/ucare\.timepad\.ru\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\//i.exec(activeSrc ?? "")?.[1] : undefined;
  const previewSrc = timepadId ? `https://ucare.timepad.ru/${timepadId}/-/preview/64x64/` : undefined;
  const currentProgress = progressive?.key === activeKey ? progressive : null;
  const showingPreview = Boolean(previewSrc && currentProgress?.phase !== "full");
  const displaySrc = showingPreview ? previewSrc : currentProgress?.src ?? activeSrc;
  useEffect(() => {
    if (!previewSrc || !activeSrc || !imageRef.current) return;
    let cancelled = false;
    let observer: IntersectionObserver | undefined;
    const loadFull = () => {
      observer?.disconnect();
      setProgressive({ key: activeKey, phase: "loading" });
      const full = new Image();
      if (srcSet) { full.srcset = srcSet; full.sizes = props.sizes ?? "100vw"; }
      full.onload = () => { if (!cancelled) setProgressive({ key: activeKey, phase: "full", src: full.currentSrc || activeSrc }); };
      full.onerror = () => { if (!cancelled) setFailure({ key, status: "fallback" }); };
      full.src = activeSrc;
    };
    if (typeof IntersectionObserver === "undefined") loadFull();
    else {
      observer = new IntersectionObserver((entries) => { if (entries.some((entry) => entry.isIntersecting)) loadFull(); }, { rootMargin: "200px" });
      observer.observe(imageRef.current);
    }
    return () => { cancelled = true; observer?.disconnect(); };
  }, [activeKey, activeSrc, key, previewSrc, props.sizes, srcSet]);
  if (status === "placeholder") return <span className={`${styles.eventImage} ${className}`} role={alt ? "img" : undefined} aria-label={alt || undefined} aria-hidden={alt ? undefined : true} />;
  return <img ref={imageRef} alt={alt} className={`${styles.eventImage} ${showingPreview ? previewLoadedKey === activeKey ? "" : styles.eventImageLoading : loadedKey === activeKey ? "" : styles.eventImageLoading} ${className}`} width={width} height={height} loading="lazy" {...props} src={displaySrc} srcSet={showingPreview ? undefined : status === "original" && !currentProgress?.src ? srcSet : undefined} onLoad={(event) => {
    if (showingPreview) {
      setPreviewLoadedKey(activeKey);
      return;
    }
    props.onLoad?.(event);
    setLoadedKey(activeKey);
  }} onError={(event) => {
    if (showingPreview) { setProgressive({ key: activeKey, phase: "full" }); return; }
    onError?.(event);
    if (status === "fallback" || event.currentTarget.src === new URL(fallbackSrc, document.baseURI).href) setFailure({ key, status: "placeholder" });
    else setFailure({ key, status: "fallback" });
  }} />;
}

export type EventCardData = { id?: string; title: string; meta?: string; eyebrow?: string; image: string; fallbackImage?: string };
export function EventCard({ event, compact = false, onClick, className = "" }: { event: EventCardData; compact?: boolean; onClick?: () => void; className?: string }) {
  const content = <><EventImage src={event.image} fallbackSrc={event.fallbackImage} alt="" /><span className={styles.eventCopy}><span className={styles.eyebrow}>{event.eyebrow ?? "СОБЫТИЕ"}</span><strong>{event.title}</strong>{event.meta && <span>{event.meta}</span>}</span><span className={styles.arrow} aria-hidden="true">›</span></>;
  return onClick ? <button type="button" className={`${styles.eventCard} ${compact ? styles.compact : ""} ${className}`} onClick={onClick}>{content}</button> : <article className={`${styles.eventCard} ${compact ? styles.compact : ""} ${className}`}>{content}</article>;
}

export function PageShell({ children, className = "", withBottomNav = false }: { children: ReactNode; className?: string; withBottomNav?: boolean }) {
  return <main className={`${styles.pageShell} ${withBottomNav ? styles.pageShellWithNav : ""} ${className}`}>{children}</main>;
}

export function PageContent({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`${styles.pageContent} ${className}`}>{children}</div>;
}

export function TopBar({ title, onBack, right, prominentBack = false, spacious = false }: { title?: ReactNode; onBack?: () => void; right?: ReactNode; prominentBack?: boolean; spacious?: boolean }) {
  return <header className={`${styles.topBar} ${spacious ? styles.topBarSpacious : ""}`}><div className={styles.topBarSide}>{onBack && <IconButton label="Назад" variant={prominentBack ? "surface" : "default"} className={prominentBack ? styles.prominentBack : ""} onClick={onBack}><ArrowLeftIcon /></IconButton>}</div><div className={styles.topBarTitle}>{title}</div><div className={styles.topBarSide}>{right}</div></header>;
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

export type ScreenSkeletonVariant = "home" | "event" | "cards" | "form" | "room" | "map" | "generic";
type ScreenSkeletonProps = { variant?: ScreenSkeletonVariant; inline?: boolean; label?: string };
export function ScreenSkeleton({ variant = "generic", inline = false, label = "Загрузка содержимого" }: ScreenSkeletonProps) {
  const heading = <span className={`${styles.skeleton} ${styles.screenHeading}`} />;
  const line = (wide = false) => <span className={`${styles.skeleton} ${styles.screenLine} ${wide ? styles.screenLineWide : ""}`} />;
  const image = (className = "") => <span className={`${styles.skeleton} ${styles.screenImage} ${className}`} />;
  const card = (index: number) => <div className={styles.screenCard} key={index}>{image()}<div className={styles.screenCardCopy}>{line(true)}{line()}{line()}</div></div>;
  let content: ReactNode;
  switch (variant) {
    case "home": content = <>{heading}{image(styles.screenHero)}<div className={styles.screenChips}>{[0, 1, 2].map(i => <span key={i} className={`${styles.skeleton} ${styles.screenChip}`} />)}</div><div className={styles.screenCards}>{[0, 1, 2].map(card)}</div></>; break;
    case "event": content = <>{image(styles.screenHero)}{heading}{line(true)}<div className={styles.screenPanel}>{line(true)}{line()}{line()}</div><span className={`${styles.skeleton} ${styles.screenButton}`} /></>; break;
    case "cards": content = <>{heading}<div className={styles.screenCards}>{[0, 1, 2, 3].map(card)}</div></>; break;
    case "form": content = <>{heading}{[0, 1, 2].map(i => <div className={styles.screenField} key={i}>{line()}{image(styles.screenInput)}</div>)}<span className={`${styles.skeleton} ${styles.screenButton}`} /></>; break;
    case "room": content = <>{heading}<div className={styles.screenPanel}>{line(true)}{line()}{line()}</div><div className={styles.screenActions}><span className={`${styles.skeleton} ${styles.screenButton}`} /><span className={`${styles.skeleton} ${styles.screenButton}`} /></div><div className={styles.screenCards}>{[0, 1].map(card)}</div></>; break;
    case "map": content = <>{heading}{image(styles.screenMap)}<div className={styles.screenCards}>{[0, 1].map(card)}</div></>; break;
    default: content = <>{heading}{line(true)}{line()}<div className={styles.screenPanel}>{line(true)}{line()}</div><div className={styles.screenCards}>{[0, 1].map(card)}</div></>;
  }
  return <div className={`${styles.screenSkeleton} ${inline ? styles.screenSkeletonInline : styles.screenSkeletonPage}`} role="status" aria-label={label} aria-busy="true"><div className={`${styles.screenSkeletonContent} ${inline ? "" : styles.pageContent}`}>{content}</div></div>;
}

export type NavIconName = "home" | "calendar" | "saved";
export type NavItem = { id: string; label: string; icon: NavIconName };
function NavIcon({ name }: { name: NavIconName }) {
  if (name === "home") return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="m4 10 8-6 8 6v9a1 1 0 0 1-1 1h-4v-6H9v6H5a1 1 0 0 1-1-1Z" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" /></svg>;
  if (name === "calendar") return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><rect x="4" y="5.5" width="16" height="14" rx="2" fill="none" stroke="currentColor" strokeWidth="1.8" /><path d="M8 3.5v4M16 3.5v4M4 10h16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></svg>;
  return <HeartIcon />;
}
export function BottomNav({ items, activeId, onChange }: { items: NavItem[]; activeId?: string; onChange?: (id: string) => void }) {
  const [lastClick, setLastClick] = useState<NavClickToken | null>(() => pendingNavClick && pendingNavClick.sequence > consumedNavClickSequence ? pendingNavClick : null);
  useEffect(() => {
    if (lastClick && pendingNavClick?.sequence === lastClick.sequence) consumedNavClickSequence = lastClick.sequence;
  }, [lastClick]);
  useEffect(() => {
    if (!lastClick) return;
    const timeout = window.setTimeout(() => {
      setLastClick((current) => current?.sequence === lastClick.sequence ? null : current);
    }, 720);
    return () => window.clearTimeout(timeout);
  }, [lastClick]);
  return <nav className={styles.bottomNav} aria-label="Основная навигация">{items.map(item => {
    const clicked = lastClick?.id === item.id;
    return <button type="button" key={item.id} className={`${styles.navItem} ${activeId === item.id ? styles.active : ""} ${clicked ? styles[`navClick_${item.icon}`] : ""}`} aria-current={activeId === item.id ? "page" : undefined} onClick={() => {
      const token = { id: item.id, sequence: ++navClickSequence };
      pendingNavClick = token;
      setLastClick(token);
      onChange?.(item.id);
    }}><span key={clicked ? `icon-${lastClick?.sequence}` : "icon"} className={`${styles.navIcon} ${styles[`navIcon_${item.icon}`]}`}><NavIcon name={item.icon} /></span><span key={clicked ? `label-${lastClick?.sequence}` : "label"} className={styles.navLabel}>{item.label}</span></button>;
  })}</nav>;
}
