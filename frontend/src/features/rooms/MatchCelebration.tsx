import { motion, useReducedMotion } from 'motion/react'
import type { EventDetail, PublicParticipant } from '../../shared/api/types'
import { eventImage, eventImageFallback } from '../../shared/lib/events'
import { EventImage } from '../../shared/ui'
import { participantInitials } from './animation'
import styles from './match.module.css'

const particles = Array.from({ length: 10 }, (_, index) => index)

export function MatchCelebration({ event, participants = [] }: { event: EventDetail; participants?: PublicParticipant[] }) {
  const reducedMotion = useReducedMotion()
  const people = participants.length ? participants.slice(0, 2) : [{ displayName: 'И' }, { displayName: 'А' }]
  return (
    <div className={styles.stage}>
      {!reducedMotion && <div className={styles.confetti} aria-hidden="true">{particles.map((particle) => <motion.i key={particle} initial={{ opacity: 0, x: 0, y: 0, scale: 0 }} animate={{ opacity: [0, 1, 0], x: Math.cos(particle) * (80 + particle * 3), y: Math.sin(particle) * (80 + particle * 2), scale: [0, 1, .5], rotate: particle * 42 }} transition={{ duration: .78, delay: particle * .018, ease: 'easeOut' }} />)}</div>}
      <motion.div className={styles.avatars} initial={reducedMotion ? false : { scale: .5, opacity: 0 }} animate={reducedMotion ? undefined : { scale: 1, opacity: 1 }} transition={{ type: 'spring', stiffness: 220, damping: 17 }}><span>{participantInitials(people[0].displayName)}</span><b>♥</b><span>{participantInitials(people[1]?.displayName ?? 'А')}</span></motion.div>
      <motion.p initial={reducedMotion ? false : { opacity: 0, y: 12 }} animate={reducedMotion ? undefined : { opacity: 1, y: 0 }} transition={{ delay: .16 }}>СОБЫТИЕ ПОНРАВИЛОСЬ ВАМ ОБОИМ</motion.p>
      <motion.h1 initial={reducedMotion ? false : { opacity: 0, scale: .85 }} animate={reducedMotion ? undefined : { opacity: 1, scale: 1 }} transition={{ delay: .24, type: 'spring' }}>Это мэтч!</motion.h1>
      <motion.article className={styles.card} initial={reducedMotion ? false : { opacity: 0, y: 24 }} animate={reducedMotion ? undefined : { opacity: 1, y: 0 }} transition={{ delay: .34, duration: .32 }}>
        <EventImage className={styles.cardImage} src={eventImage(event.imageUrl, event.category_slug)} fallbackSrc={eventImageFallback(event.category_slug)} alt={event.title} />
        <div><small>{event.date_label} · {event.venue_name}</small><h2>{event.title}</h2><strong>{event.price_label}</strong></div>
      </motion.article>
    </div>
  )
}
