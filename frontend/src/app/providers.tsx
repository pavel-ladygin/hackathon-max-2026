import { MaxUI } from '@maxhub/max-ui'
import '@maxhub/max-ui/dist/styles.css'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { useEffect, type ReactNode } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { installMockSync } from '../mocks/state'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 20_000, retry: 1, refetchOnWindowFocus: true },
    mutations: { retry: 0 },
  },
})

function MockSync() {
  const client = useQueryClient()
  useEffect(() => {
    if ((import.meta.env.VITE_API_MODE ?? 'mock') !== 'mock') return
    return installMockSync(() => {
      void client.invalidateQueries({ queryKey: ['room'] })
      void client.invalidateQueries({ queryKey: ['bootstrap'] })
      void client.invalidateQueries({ queryKey: ['home-feed'] })
    })
  }, [client])
  return null
}

export function AppProviders({ children }: { children: ReactNode }) {
  return (
    <MaxUI>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <MockSync />
          {children}
        </BrowserRouter>
      </QueryClientProvider>
    </MaxUI>
  )
}
