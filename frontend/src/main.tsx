import React from 'react'
import ReactDOM from 'react-dom/client'
import { App } from './app/App'
import { AnalyticsDashboard } from './pages/internal/AnalyticsDashboard'
import { EventSourcesPage } from './pages/internal/EventSourcesPage'
import './app/styles/base.css'

// Keep the internal dashboard out of the MAX application tree: the proxy
// authenticates this path and the page does not need MAX bootstrap or bridge.
const isAnalyticsDashboard = window.location.pathname.replace(/\/$/, '') === '/internal/analytics'
const isEventSourcesPage = window.location.pathname.replace(/\/$/, '') === '/internal/event-sources'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {isAnalyticsDashboard ? <AnalyticsDashboard /> : isEventSourcesPage ? <EventSourcesPage /> : <App />}
  </React.StrictMode>,
)
