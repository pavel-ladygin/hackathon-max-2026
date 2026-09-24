package discovery

import (
	"testing"

	"github.com/google/uuid"
)

func TestClusterMapEventsGroupsByWorldPixelCell(t *testing.T) {
	latA, lngA := 55.75, 37.61
	latB, lngB := 55.75001, 37.61001
	latC, lngC := 55.75, 37.7
	items := clusterMapEvents([]Card{
		{ID: uuid.New(), Latitude: &latA, Longitude: &lngA},
		{ID: uuid.New(), Latitude: &latB, Longitude: &lngB},
		{ID: uuid.New(), Latitude: &latC, Longitude: &lngC},
	}, 12)
	if len(items) != 2 {
		t.Fatalf("got %d map items, want a cluster and singleton: %+v", len(items), items)
	}
	cluster, err := items[0].AsEventMapCluster()
	if err != nil || cluster.Kind != "cluster" || cluster.Count != 2 || cluster.North == 0 || cluster.South == 0 || cluster.West == 0 || cluster.East == 0 {
		t.Fatalf("cluster item lacks exact count or bounds: %+v (%v)", cluster, err)
	}
	point, err := items[1].AsEventMapPoint()
	if err != nil || point.Kind != "event" || point.Id == uuid.Nil || point.Event.Id != point.Id {
		t.Fatalf("singleton item lacks event card: %+v (%v)", point, err)
	}
	again := clusterMapEvents([]Card{{ID: uuid.New(), Latitude: &latA, Longitude: &lngA}, {ID: uuid.New(), Latitude: &latB, Longitude: &lngB}}, 12)
	clusterAgain, err := again[0].AsEventMapCluster()
	if err != nil || clusterAgain.Id != cluster.Id {
		t.Fatalf("cluster ID changed for same zoom/cell: %q != %q (%v)", clusterAgain.Id, cluster.Id, err)
	}
}

func TestClusterBoundsRetainPrecisionAtMaximumZoom(t *testing.T) {
	lat, lng := 55.75, 37.61
	items := clusterMapEvents([]Card{
		{ID: uuid.New(), Latitude: &lat, Longitude: &lng},
		{ID: uuid.New(), Latitude: &lat, Longitude: &lng},
	}, 22)
	if len(items) != 1 {
		t.Fatalf("got %d map items, want one cluster", len(items))
	}
	cluster, err := items[0].AsEventMapCluster()
	if err != nil || cluster.East <= cluster.West || cluster.North <= cluster.South {
		t.Fatalf("maximum-zoom cluster has invalid bounds: %+v (%v)", cluster, err)
	}
}
