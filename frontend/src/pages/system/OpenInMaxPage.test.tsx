import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { OpenInMaxPage } from './OpenInMaxPage'
import { maxPlatform } from '../../shared/platform/max/adapter'

describe('OpenInMaxPage', () => {
  afterEach(() => vi.restoreAllMocks())

  it('opens MAX with the requested event start parameter', () => {
    const openMaxLink = vi.spyOn(maxPlatform, 'openMaxLink').mockResolvedValue(true)
    render(<OpenInMaxPage startParam="event_6dcd4ce2-8f2a-4d3e-a8b7-1ef42acfc1a1" />)

    fireEvent.click(screen.getByRole('button', { name: 'Открыть в MAX' }))

    expect(openMaxLink).toHaveBeenCalledWith('https://max.ru?startapp=event_6dcd4ce2-8f2a-4d3e-a8b7-1ef42acfc1a1')
  })
})
