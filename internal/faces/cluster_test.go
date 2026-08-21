package faces

import (
	"math/rand"
	"testing"

	"pocket-nas/internal/faces/ort"
)

// randVec returns a unit-norm-ish vector around a base direction.
func randVec(r *rand.Rand, base []float32, noise float32) []float32 {
	v := make([]float32, len(base))
	for i, b := range base {
		v[i] = b + noise*(r.Float32()-0.5)
	}
	return l2normalize(v)
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	if s := CosineSimilarity(a, b); s < 0.999 {
		t.Fatalf("identical vectors: %v", s)
	}
	c := []float32{0, 1, 0}
	if s := CosineSimilarity(a, c); s > 0.001 || s < -0.001 {
		t.Fatalf("orthogonal vectors: %v", s)
	}
	if s := CosineSimilarity(a, nil); s != 0 {
		t.Fatalf("mismatched lengths: %v", s)
	}
}

func TestAssignClusterSynthetic(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	base1 := randVec(r, make([]float32, 512), 2) // random direction
	base2raw := make([]float32, 512)
	for i := range base2raw {
		base2raw[i] = r.Float32() - 0.5
	}
	base2 := l2normalize(base2raw)

	// Two clusters with tight members, one outlier.
	clusters := []ClusterInfo{
		{ID: 1, PersonID: 1, Centroid: base1, Count: 3},
		{ID: 2, PersonID: 2, Centroid: base2, Count: 2},
	}
	member1 := randVec(r, base1, 0.1)
	id, sim, ok := AssignCluster(member1, clusters, 0.5)
	if !ok || id != 1 {
		t.Fatalf("member of cluster 1 got id=%d sim=%v ok=%v", id, sim, ok)
	}
	member2 := randVec(r, base2, 0.1)
	if id, _, ok := AssignCluster(member2, clusters, 0.5); !ok || id != 2 {
		t.Fatalf("member of cluster 2 got id=%d ok=%v", id, ok)
	}
	outlier := randVec(r, make([]float32, 512), 2)
	// Ensure the outlier is actually far from both centroids.
	if CosineSimilarity(outlier, base1) >= 0.5 || CosineSimilarity(outlier, base2) >= 0.5 {
		t.Skip("unlucky random draw")
	}
	if id, _, ok := AssignCluster(outlier, clusters, 0.5); ok || id != 0 {
		t.Fatalf("outlier got id=%d ok=%v", id, ok)
	}
}

func TestEstimateSimilarityIdentity(t *testing.T) {
	pts := [5][2]float32{{0, 0}, {10, 0}, {5, 5}, {2, 8}, {8, 8}}
	m := estimateSimilarity(pts, pts)
	for i, p := range pts {
		x := m.a*float64(p[0]) - m.b*float64(p[1]) + m.tx
		y := m.b*float64(p[0]) + m.a*float64(p[1]) + m.ty
		if d := (x-float64(p[0]))*(x-float64(p[0])) + (y-float64(p[1]))*(y-float64(p[1])); d > 1e-6 {
			t.Fatalf("identity transform maps %v to (%v,%v) (pt %d)", p, x, y, i)
		}
	}
}

func TestEstimateSimilarityScale(t *testing.T) {
	src := [5][2]float32{{0, 0}, {10, 0}, {5, 5}, {2, 8}, {8, 8}}
	var dst [5][2]float32
	for i, p := range src { // scale by 2, translate by (3, 4)
		dst[i] = [2]float32{2*p[0] + 3, 2*p[1] + 4}
	}
	m := estimateSimilarity(src, dst)
	for i, p := range src {
		x := m.a*float64(p[0]) - m.b*float64(p[1]) + m.tx
		y := m.b*float64(p[0]) + m.a*float64(p[1]) + m.ty
		dx, dy := x-float64(dst[i][0]), y-float64(dst[i][1])
		if dx*dx+dy*dy > 1e-6 {
			t.Fatalf("scaled transform off at pt %d: got (%v,%v) want %v", i, x, y, dst[i])
		}
	}
}

func TestNMS(t *testing.T) {
	faces := []Face{
		{Box: [4]float32{0, 0, 100, 100}, Score: 0.9},
		{Box: [4]float32{5, 5, 105, 105}, Score: 0.8}, // dup
		{Box: [4]float32{200, 200, 300, 300}, Score: 0.7},
	}
	kept := nms(faces, 0.4)
	if len(kept) != 2 {
		t.Fatalf("nms kept %d faces, want 2", len(kept))
	}
	if kept[0].Score != 0.9 {
		t.Fatalf("highest score face dropped")
	}
}

func TestClassifyDetOutputs(t *testing.T) {
	// SCRFD-style outputs with opaque names, intentionally unordered.
	mk := func(a, b int64) ort.Tensor { return ort.Tensor{Shape: []int64{a, b}, Data: nil} }
	out, err := classifyDetOutputs(map[string]ort.Tensor{
		"471": mk(3200, 4), "443": mk(12800, 1), "499": mk(800, 10),
		"446": mk(12800, 4), "493": mk(800, 1), "468": mk(3200, 1),
		"449": mk(12800, 10), "474": mk(3200, 10), "496": mk(800, 4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.scores) != 3 || len(out.boxes) != 3 || len(out.kps) != 3 {
		t.Fatalf("classification: %d/%d/%d", len(out.scores), len(out.boxes), len(out.kps))
	}
	// Sorted stride 8 first (most cells).
	if out.scores[0].Shape[0] != 12800 || out.boxes[2].Shape[0] != 800 {
		t.Fatalf("stride order wrong: %v %v", out.scores[0].Shape, out.boxes[2].Shape)
	}
}
