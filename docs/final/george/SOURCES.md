# Provider evidence

Checked 2026-10-20.

- The official [KudaGo API contract](https://docs.kudago.com/api/) explicitly
  lists `kzn` for Kazan in the event `location` filter. City metadata endpoints
  expose timezone and coordinates. This confirms a documented location code,
  **not current future-event coverage**.
- A bounded public request to
  `https://kudago.com/public-api/v1.4/locations/kzn/?lang=ru&fields=slug,name,timezone,coords`
  did not return usable metadata in this environment (sandbox request failure,
  then HTTP failure in the user context). No restriction bypass was attempted.
- [GeoNames city 551487](https://www.geonames.org/551487/kazan.html) identifies
  Kazan at 55.78874, 49.12214 with timezone Europe/Moscow. These are a city
  reference point, not a provider venue or geofenced municipal boundary.
- No Timepad credential was read or requested and no authenticated call made.
  Its non-Moscow live coverage remains unverified.

An isolated Kazan fixture PoC is permitted by the task. Live coverage requires
an approved provider response and a measured future catalog before activation.

## Configuration after dev sync (2026-10-21)
Metadata migration: 000031_multicity_city.sql (after released25/30). Worker validates city UUID/name/timezone and KudaGo/Timepad profile. Additional cities require EVENT_SYNC_CITY_PROFILES; unknown free-form provider locations fail closed. This configuration is not evidence of live coverage. No new provider call was made during fixes.
