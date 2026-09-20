import { useNavigate } from 'react-router-dom'
import { Button, Empty, PageContent, PageShell } from '../../shared/ui/index'

/** Placeholder while the rooms backend flow is being implemented. */
export function RoomUnavailablePage() {
  const navigate = useNavigate()

  return (
    <PageShell>
      <PageContent>
        <Empty
          title="Сценарий комнат временно недоступен"
          description="Сценарий совместного выбора скоро вернётся. А пока можно посмотреть афишу и сохранить интересные события."
          action={<Button onClick={() => navigate('/events')}>Открыть афишу</Button>}
        />
      </PageContent>
    </PageShell>
  )
}
