import { maxPlatform } from '../../shared/platform/max/adapter'
import { Button, PageContent, PageShell } from '../../shared/ui/index'
import styles from '../pages.module.css'

export function OpenInMaxPage({ error }: { error?: string }) {
  const appUrl = import.meta.env.VITE_MAX_APP_URL ?? 'https://max.ru'
  return (
    <PageShell>
      <PageContent className={`${styles.narrow} ${styles.center}`}>
        <div className={styles.avatars} aria-hidden="true"><span className={styles.avatar}>В</span><span className={styles.avatar}>М</span></div>
        <p className={styles.eyebrow}>ВМЕСТЕ В MAX</p>
        <h1 className={styles.title}>Выберите событие вдвоём</h1>
        <p className={styles.subtitle}>{error ?? 'Откройте мини-приложение внутри MAX, чтобы подтвердить профиль и пригласить друга.'}</p>
        <div className={styles.footer}><Button onClick={() => void maxPlatform.openMaxLink(appUrl)}>Открыть в MAX</Button></div>
      </PageContent>
    </PageShell>
  )
}
