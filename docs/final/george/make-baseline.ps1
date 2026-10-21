# Offline-only corpus derived from the two existing submission fixture cards.
# This script does not connect to a provider or database.
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))
$seed = Get-Content -LiteralPath (Join-Path $repo 'backend/seed/submission.json') -Raw | ConvertFrom-Json
$rows = @()
for ($i = 1; $i -le 20; $i++) {
    $original = $seed.events[($i - 1) % $seed.events.Count]
    $suffix = '{0:D12}' -f $i
    $row = [ordered]@{
        id = "a0000000-0000-4000-8000-$suffix"
        source = 'submission-fixture'
        external_id = "baseline-$i"
        status = $original.status
        starts_at = '2026-10-24T16:00:00Z'
        venue_id = "b0000000-0000-4000-8000-$suffix"
        category = $original.category_slug
        provider_active = $true
        ticket_available = $original.ticket_available
        ticket_url = $original.ticket_url
        description = $original.description
        price_from_minor = $original.price_minor
        latitude = $seed.venue.latitude
        longitude = $seed.venue.longitude
        images = @(@{role='card'})
    }
    # Deliberate defects/controls; proportions are synthetic, never prevalence.
    if ($i -le 4) { $row.images = @() }
    if ($i -ge 5 -and $i -le 8) { $row.price_from_minor = $null }
    if ($i -ge 9 -and $i -le 10) { $row.latitude = $null; $row.longitude = $null }
    if ($i -ge 11 -and $i -le 12) { $row.description = '' }
    if ($i -eq 13) { $row.provider_active = $false }
    if ($i -eq 14) { $row.status = 'cancelled' }
    if ($i -eq 15) { $row.starts_at = '2026-10-19T16:00:00Z' }
    if ($i -eq 16) { $row.ticket_available = $false; $row.ticket_url = $null }
    if ($i -eq 17) { $row.price_from_minor = 400000 }
    if ($i -eq 18) { $row.category = 'invalid-fixture-category' }
    if ($i -eq 19) { $row.starts_at = '2026-10-24T20:00:00Z' }
    if ($i -eq 20) { $row.price_from_minor = 0 }
    $rows += $row
}
$fixture = [ordered]@{
    label = 'SYNTHETIC: 20 controlled variants of backend/seed/submission.json; NOT Moscow live data'
    city = @{
        id = $seed.city.id; timezone = $seed.city.timezone
        center_lat = $seed.city.center_lat; center_lng = $seed.city.center_lng
    }
    events = $rows
    sync_runs = @()
}
$fixture | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $PSScriptRoot 'baseline.fixture.json') -Encoding utf8
