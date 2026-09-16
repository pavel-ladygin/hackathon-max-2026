import { motion } from 'motion/react'
import type { EventDetail } from '../../shared/api/types'
import styles from './match.module.css'

const particles = Array.from({ length: 20 }, (_, index) => index)

export function MatchCelebration({ event }: { event: EventDetail }) {
  return (
    <div className={styles.stage}>
      <div className={styles.confetti} aria-hidden="true">{particles.map((particle) => <motion.i key={particle} initial={{ opacity: 0, x: 0, y: 0, scale: 0 }} animate={{ opacity: [0, 1, 0], x: Math.cos(particle) * (80 + particle * 3), y: Math.sin(particle) * (80 + particle * 2), scale: [0, 1, .5], rotate: particle * 42 }} transition={{ duration: 1.6, delay: particle * .025, ease: 'easeOut' }} />)}</div>
      <motion.div className={styles.avatars} initial={{ scale: .5, opacity: 0 }} animate={{ scale: 1, opacity: 1 }} transition={{ type: 'spring', stiffness: 220, damping: 17 }}><span>И</span><b>♥</b><span>А</span></motion.div>
      <motion.p initial={{ opacity: 0, y: 12 }} animate={{ opacity: 1, y: 0 }} transition={{ delay: .22 }}>СОБЫТИЕ ПОНРАВИЛОСЬ ВАМ ОБОИМ</motion.p>
      <motion.h1 initial={{ opacity: 0, scale: .85 }} animate={{ opacity: 1, scale: 1 }} transition={{ delay: .32, type: 'spring' }}>Это мэтч!</motion.h1>
      <motion.article className={styles.card} initial={{ opacity: 0, y: 34, rotateX: -9 }} animate={{ opacity: 1, y: 0, rotateX: 0 }} transition={{ delay: .48, duration: .5 }}>
        <img src={event.imageUrl ?? '/events/concert-singer.png'} alt={event.title} />
        <div><small>{event.date_label} · {event.venue_name}</small><h2>{event.title}</h2><strong>{event.price_label}</strong></div>
      </motion.article>
    </div>
  )
}
