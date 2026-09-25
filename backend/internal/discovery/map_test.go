package discovery

import (
	"math"
	"testing"

	"github.com/google/uuid"
)

func TestClusterMapEventsGroupsNearbyPointsAcrossFormerGridBoundary(t *testing.T) {
	// At zoom 12 these points are about 12px apart but lie on opposite sides
	// of a former 64px world-pixel cell boundary.
	latA, lngA := 55.75, 37.5068
	latB, lngB := 55.75001, 37.5078
	latC, lngC := 55.75, 37.62
	idA, idB, idC := uuid.New(), uuid.New(), uuid.New()
	items := clusterMapEvents([]Card{
		{ID: idA, Latitude: &latA, Longitude: &lngA},
		{ID: idB, Latitude: &latB, Longitude: &lngB},
		{ID: idC, Latitude: &latC, Longitude: &lngC},
	}, 12)
	if len(items) != 2 {
		t.Fatalf("got %d map items, want a cluster and singleton", len(items))
	}
	cluster, err := items[0].AsEventMapCluster()
	if err != nil || cluster.Kind != "cluster" || cluster.Count != 2 {
		t.Fatalf("cluster item lacks exact count: %+v (%v)", cluster, err)
	}
	if len(cluster.Members) != cluster.Count {
		t.Fatalf("cluster has %d members, want %d", len(cluster.Members), cluster.Count)
	}
	clusterMemberIDs := map[uuid.UUID]bool{}
	for _, member := range cluster.Members {
		if member.Kind != "event" || member.Id != member.Event.Id || member.Latitude != member.Event.Latitude.MustGet() || member.Longitude != member.Event.Longitude.MustGet() {
			t.Fatalf("cluster member should be a complete map point: %+v", member)
		}
		clusterMemberIDs[member.Id] = true
	}
	if !clusterMemberIDs[idA] || !clusterMemberIDs[idB] {
		t.Fatalf("cluster members should include both clustered events: %v", clusterMemberIDs)
	}
	if cluster.Longitude < lngA || cluster.Longitude > lngB || cluster.Latitude < latA || cluster.Latitude > latB {
		t.Fatalf("cluster marker should be at member centroid, got (%f,%f)", cluster.Latitude, cluster.Longitude)
	}
	if cluster.West != lngA || cluster.East != lngB || cluster.South != latA || cluster.North != latB {
		t.Fatalf("cluster bounds should enclose members: %+v", cluster)
	}
	point, err := items[1].AsEventMapPoint()
	if err != nil || point.Kind != "event" || point.Id != idC || point.Event.Id != point.Id {
		t.Fatalf("singleton item lacks event card: %+v (%v)", point, err)
	}
	again := clusterMapEvents([]Card{{ID: idA, Latitude: &latA, Longitude: &lngA}, {ID: idB, Latitude: &latB, Longitude: &lngB}}, 12)
	clusterAgain, err := again[0].AsEventMapCluster()
	if err != nil || clusterAgain.Id != cluster.Id {
		t.Fatalf("cluster ID changed for same members: %q != %q (%v)", clusterAgain.Id, cluster.Id, err)
	}
}

func TestMaximumZoomReturnsCoincidentEventsAsSeparatePoints(t *testing.T) {
	lat, lng := 55.75, 37.61
	idA, idB := uuid.New(), uuid.New()
	items := clusterMapEvents([]Card{
		{ID: idA, Latitude: &lat, Longitude: &lng},
		{ID: idB, Latitude: &lat, Longitude: &lng},
	}, 22)
	if len(items) != 2 {
		t.Fatalf("got %d map items, want two event points", len(items))
	}
	seen := map[uuid.UUID]bool{}
	for _, item := range items {
		point, err := item.AsEventMapPoint()
		if err != nil || point.Kind != "event" {
			t.Fatalf("maximum zoom item should be a point: %+v (%v)", item, err)
		}
		seen[point.Id] = true
	}
	if !seen[idA] || !seen[idB] {
		t.Fatalf("maximum zoom omitted coincident event IDs: %v", seen)
	}
}

func TestClusterMapEventsDoesNotJoinLongProximityChains(t *testing.T) {
	world := 256.0 * math.Pow(2, 12)
	baseLng := 37.5
	lat := 55.75
	events := make([]Card, 0, 5)
	for i := 0; i < 5; i++ {
		lng := baseLng + float64(i*50)/world*360
		events = append(events, Card{ID: uuid.New(), Latitude: &lat, Longitude: &lng})
	}
	items := clusterMapEvents(events, 12)
	if len(items) != 3 {
		t.Fatalf("got %d items, want two bounded clusters and one event", len(items))
	}
	for _, item := range items {
		if cluster, err := item.AsEventMapCluster(); err == nil && cluster.Count > 2 {
			t.Fatalf("proximity chain formed an oversized cluster: %+v", cluster)
		}
	}
}

func TestClusterMapEventsHandlesAntimeridianNeighbors(t *testing.T) {
	latA, latB := 10.0, 10.00001
	lngA, lngB := 179.999, -179.999
	items := clusterMapEvents([]Card{
		{ID: uuid.New(), Latitude: &latA, Longitude: &lngA},
		{ID: uuid.New(), Latitude: &latB, Longitude: &lngB},
	}, 12)
	if len(items) != 1 {
		t.Fatalf("got %d items, want one antimeridian cluster", len(items))
	}
	cluster, err := items[0].AsEventMapCluster()
	if err != nil {
		t.Fatalf("expected cluster, got error: %v", err)
	}
	if math.Abs(math.Abs(cluster.Longitude)-180) > 0.01 {
		t.Fatalf("centroid should be near the antimeridian, got longitude %f", cluster.Longitude)
	}
	if cluster.Longitude < -180 || cluster.Longitude > 180 {
		t.Fatalf("centroid longitude is outside [-180,180]: %f", cluster.Longitude)
	}
	if cluster.West < cluster.East {
		t.Fatalf("bounds should cross the antimeridian, got west=%f east=%f", cluster.West, cluster.East)
	}
}
