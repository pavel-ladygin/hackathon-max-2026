import { MaxUI } from '@maxhub/max-ui'
import '@maxhub/max-ui/dist/styles.css'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { type ReactNode } from 'react'
import { BrowserRouter } from 'react-router-dom'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 20_000, retry: 1, refetchOnWindowFocus: true },
    mutations: { retry: 0 },
  },
})

export function AppProviders({ children }: { children: ReactNode }) {
  return (
    <MaxUI>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          {children}
        </BrowserRouter>
      </QueryClientProvider>
    </MaxUI>
  )
}
