export function MockScenarioPanel() {
  const current = sessionStorage.getItem('max-together-mock-scenario') ?? new URLSearchParams(location.search).get('mockScenario') ?? 'normal'
  return <label className="mock-scenario-panel">Mock
    <select value={current} onChange={(event) => {
      const value = event.target.value
      sessionStorage.setItem('max-together-mock-scenario', value)
      const url = new URL(location.href)
      if (value === 'normal') url.searchParams.delete('mockScenario')
      else url.searchParams.set('mockScenario', value)
      location.assign(url)
    }}>
      <option value="normal">обычный</option>
      <option value="rate-limited">429 rate limit</option>
      <option value="internal">500 internal</option>
      <option value="token-expired">401 token expired</option>
    </select>
  </label>
}
