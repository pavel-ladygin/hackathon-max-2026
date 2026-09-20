package catalog

import (
	"math"

	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const earthRadiusMeters = 6_371_000.0

// HaversineMeters returns the great-circle distance between two WGS 84 points.
func HaversineMeters(latitudeA, longitudeA, latitudeB, longitudeB float64) float64 {
	latitudeDelta := radians(latitudeB - latitudeA)
	longitudeDelta := radians(longitudeB - longitudeA)
	latitudeA = radians(latitudeA)
	latitudeB = radians(latitudeB)

	a := math.Sin(latitudeDelta/2)*math.Sin(latitudeDelta/2) +
		math.Cos(latitudeA)*math.Cos(latitudeB)*math.Sin(longitudeDelta/2)*math.Sin(longitudeDelta/2)
	// Floating-point rounding can put a just outside [0, 1] near antipodes.
	a = math.Max(0, math.Min(1, a))
	return earthRadiusMeters * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// NearestMetroDistance returns the distance to the closest station. Its second
// result is false when stations is empty, making missing metro data explicit.
func NearestMetroDistance(latitude, longitude float64, stations []platform.MetroStation) (float64, bool) {
	if len(stations) == 0 {
		return 0, false
	}

	nearest := math.Inf(1)
	for _, station := range stations {
		distance := HaversineMeters(latitude, longitude, station.Latitude, station.Longitude)
		if distance < nearest {
			nearest = distance
		}
	}
	return nearest, true
}

func radians(degrees float64) float64 { return degrees * math.Pi / 180 }
