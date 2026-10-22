export type TextSize = 'compact' | 'normal' | 'large'
const KEY = 'max-text-size'
export function readTextSize(): TextSize {
  try { const value = localStorage.getItem(KEY); return value === 'compact' || value === 'large' ? value : 'normal' } catch { return 'normal' }
}
export function setTextSize(value: TextSize) {
  document.documentElement.style.setProperty('--font-scale', value === 'compact' ? '.9' : value === 'large' ? '1.15' : '1')
  try { localStorage.setItem(KEY, value) } catch { /* keep the in-memory choice */ }
}
export function initializeTextSize() { setTextSize(readTextSize()) }
