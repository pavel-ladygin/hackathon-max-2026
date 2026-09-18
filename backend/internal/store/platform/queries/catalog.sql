-- name: GetCatalogCity :one
SELECT *
FROM cities
WHERE id = $1;

-- name: ListCatalogMetroStations :many
SELECT *
FROM metro_stations
WHERE city_id = $1
ORDER BY name, id;

-- name: ListCatalogVenues :many
SELECT *
FROM venues
WHERE city_id = $1
ORDER BY name, id;

-- name: ListCatalogEvents :many
SELECT e.*
FROM events AS e
JOIN venues AS v ON v.id = e.venue_id
WHERE v.city_id = $1
ORDER BY e.starts_at, e.id;

-- name: ListCatalogCategories :many
SELECT ec.*
FROM event_categories AS ec
JOIN events AS e ON e.id = ec.event_id
JOIN venues AS v ON v.id = e.venue_id
WHERE v.city_id = $1
ORDER BY ec.event_id, ec.is_primary DESC, ec.category_slug;

-- name: ListCatalogImages :many
SELECT ei.*
FROM event_images AS ei
JOIN events AS e ON e.id = ei.event_id
JOIN venues AS v ON v.id = e.venue_id
WHERE v.city_id = $1
ORDER BY ei.event_id, ei.position, ei.id;
