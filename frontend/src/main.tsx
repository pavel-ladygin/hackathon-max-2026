import React from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './app/App'
import './app/styles/base.css'

async function enableMocking() {
  if ((import.meta.env.VITE_API_MODE ?? 'mock') !== 'mock') return
  const { worker } = await import('./mocks/browser')
  await worker.start({ onUnhandledRequest: 'bypass' })
}

await enableMocking()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
