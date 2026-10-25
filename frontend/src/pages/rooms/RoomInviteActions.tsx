import type { ReactNode } from 'react'
import { Button } from '../../shared/ui/index'
import styles from '../pages.module.css'
import type { useRoomInvite } from './useRoomInvite'

type RoomInviteActionsProps = ReturnType<typeof useRoomInvite> & { className?: string; children?: ReactNode; showShareOpenedStatus?: boolean }

export function RoomInviteActions({ rawUrl, maxLink, status, busy, share, copy, className = '', children, showShareOpenedStatus = true }: RoomInviteActionsProps) {
  return <>
    <div aria-live="polite">{status === 'opened' && showShareOpenedStatus ? <p>Открыли отправку в MAX</p> : status === 'cancelled' ? <p>Отправка отменена. Можно попробовать снова или скопировать ссылку.</p> : status === 'copied' ? <p>✓ Ссылка скопирована</p> : status === 'share-error' ? <p className={styles.error} role="alert">Не удалось открыть отправку. Попробуйте ещё раз или скопируйте ссылку.</p> : status === 'copy-error' ? <p className={styles.error} role="alert">Не удалось скопировать ссылку. Вы можете скопировать её вручную выше.</p> : null}</div>
    <div className={className}>
      <Button disabled={!maxLink || busy} state={status === 'sharing' ? 'loading' : 'idle'} loadingLabel="Открываем MAX…" onClick={() => void share()}>Пригласить через MAX</Button>
      <Button tone="secondary" disabled={!rawUrl || busy} state={status === 'copying' ? 'loading' : 'idle'} loadingLabel="Копируем…" onClick={() => void copy()}>Скопировать ссылку</Button>
      {children}
    </div>
  </>
}
