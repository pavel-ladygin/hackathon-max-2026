import { ApiError } from '../../shared/api/errors'

const ROOM_ERROR_MESSAGES: Record<string, string> = {
  ACTIVE_ROOM_EXISTS: 'У вас уже есть активная комната. Завершите её перед новым выбором.',
  INVITE_EXPIRED: 'Срок приглашения истёк. Попросите друга отправить новую ссылку.',
  ROOM_FULL: 'Комната уже заполнена.',
  POOL_NOT_READY: 'Общая подборка ещё формируется. Мы обновим её автоматически.',
  POOL_EXHAUSTED: 'Карточки этого раунда закончились.',
  STALE_POOL_VERSION: 'Подборка обновилась. Загружаем актуальные карточки.',
  VOTE_ALREADY_CAST: 'Этот голос уже сохранён.',
  VALIDATION_FAILED: 'Проверьте введённые данные.',
  NOT_FOUND: 'Комната или приглашение не найдены.',
}

export function roomErrorCode(error: unknown) { return error instanceof ApiError ? error.code : null }
export function roomErrorMessage(error: unknown, fallback = 'Что-то пошло не так. Попробуйте ещё раз.') {
  const code = roomErrorCode(error)
  return (code && ROOM_ERROR_MESSAGES[code]) || (error instanceof ApiError && error.message) || fallback
}
export function isRoomError(error: unknown, code: string) { return roomErrorCode(error) === code }
