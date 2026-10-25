/** A readable Russian label for a time zone, for display in user-facing UI. */
const RUSSIAN_TIME_ZONE_LABELS: Readonly<Record<string, string>> = {
  'Europe/Kaliningrad': 'Калининградское время',
  'Europe/Moscow': 'Московское время',
  'Europe/Samara': 'Самарское время',
  'Asia/Yekaterinburg': 'Екатеринбургское время',
  'Asia/Omsk': 'Омское время',
  'Asia/Novosibirsk': 'Новосибирское время',
  'Asia/Krasnoyarsk': 'Красноярское время',
  'Asia/Irkutsk': 'Иркутское время',
  'Asia/Chita': 'Читинское время',
  'Asia/Yakutsk': 'Якутское время',
  'Asia/Vladivostok': 'Владивостокское время',
  'Asia/Magadan': 'Магаданское время',
  'Asia/Sakhalin': 'Сахалинское время',
  'Asia/Kamchatka': 'Камчатское время',
}

/** Keep IANA identifiers for date calculations; use this only for visible labels. */
export function timeZoneLabel(timezone: string): string {
  return RUSSIAN_TIME_ZONE_LABELS[timezone] ?? 'Местное время'
}
