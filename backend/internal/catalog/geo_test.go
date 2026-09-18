package catalog

import (
	"math"
	"testing"

	"github.com/google/uuid"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

func TestHaversineMeters(t *testing.T) {
	t.Run("identity", func(t *testing.T) {
		if got := HaversineMeters(55.7558, 37.6173, 55.7558, 37.6173); got != 0 {
			t.Fatalf("distance = %v, want 0", got)
		}
	})

	t.Run("symmetry", func(t *testing.T) {
		forward := HaversineMeters(55.7558, 37.6173, 59.9343, 30.3351)
		backward := HaversineMeters(59.9343, 30.3351, 55.7558, 37.6173)
		if math.Abs(forward-backward) > 1e-9 {
			t.Fatalf("forward = %v, backward = %v", forward, backward)
		}
	})

	t.Run("known distance", func(t *testing.T) {
		got := HaversineMeters(0, 0, 0, 1)
		if math.Abs(got-111_194.9266) > 0.1 {
			t.Fatalf("distance = %v, want about 111194.9266", got)
		}
	})

	t.Run("antipodes", func(t *testing.T) {
		got := HaversineMeters(0, 0, 0, 180)
		want := math.Pi * earthRadiusMeters
		if math.Abs(got-want) > 0.001 {
			t.Fatalf("distance = %v, want %v", got, want)
		}
	})
}

func TestNearestMetroDistance(t *testing.T) {
	if distance, ok := NearestMetroDistance(0, 0, nil); ok || distance != 0 {
		t.Fatalf("empty stations = (%v, %v), want (0, false)", distance, ok)
	}

	for _, test := range []struct {
		name string
		want float64
	}{
		{name: "1199 meters", want: 1199},
		{name: "1200 meters", want: 1200},
		{name: "1201 meters", want: 1201},
	} {
		t.Run(test.name, func(t *testing.T) {
			longitude := test.want / (earthRadiusMeters * math.Pi / 180)
			stations := []platform.MetroStation{
				{ID: uuid.New(), Latitude: 0, Longitude: longitude},
				{ID: uuid.New(), Latitude: 0, Longitude: longitude + 1},
			}
			got, ok := NearestMetroDistance(0, 0, stations)
			if !ok {
				t.Fatal("nearest distance reports absent metro")
			}
			if math.Abs(got-test.want) > 0.001 {
				t.Fatalf("distance = %v, want %v", got, test.want)
			}
		})
	}
}
