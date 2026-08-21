package faces

import "math"

// CosineSimilarity between two embeddings (assumed roughly unit-norm).
func CosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na < 1e-12 || nb < 1e-12 {
		return 0
	}
	return float32(dot / math.Sqrt(na*nb))
}

// AssignCluster returns the cluster whose centroid is most similar to emb
// when the best similarity reaches threshold, or (0, sim, false) when a new
// cluster should be created.
func AssignCluster(emb []float32, clusters []ClusterInfo, threshold float32) (int64, float32, bool) {
	var bestID int64
	var bestSim float32 = -1
	for _, c := range clusters {
		sim := CosineSimilarity(emb, c.Centroid)
		if sim > bestSim {
			bestSim, bestID = sim, c.ID
		}
	}
	if bestSim >= threshold {
		return bestID, bestSim, true
	}
	return 0, bestSim, false
}

// clusterForPerson maps cluster assignments to person ids.
func clusterForPerson(clusters []ClusterInfo, clusterID int64) int64 {
	for _, c := range clusters {
		if c.ID == clusterID {
			return c.PersonID
		}
	}
	return 0
}
