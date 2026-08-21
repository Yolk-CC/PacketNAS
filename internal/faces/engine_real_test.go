package faces

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/disintegration/imaging"
)

// TestRealEngineRecognition runs the full SCRFD+ArcFace pipeline against
// real models when env vars point at them (smoke-m11.sh / CI optional
// step). Default test runs skip.
func TestRealEngineRecognition(t *testing.T) {
	lib := os.Getenv("POCKETNAS_ORT_LIB")
	models := os.Getenv("POCKETNAS_FACES_MODELS")
	imgs := os.Getenv("POCKETNAS_FACES_IMAGES")
	if lib == "" || models == "" || imgs == "" {
		t.Skip("set POCKETNAS_ORT_LIB/POCKETNAS_FACES_MODELS/POCKETNAS_FACES_IMAGES to run")
	}
	eng, err := NewOnnxEngine(lib, models, BuiltinProfiles["buffalo_s"])
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	defer eng.Close()

	embed := func(name string) []float32 {
		img, err := imaging.Open(filepath.Join(imgs, name))
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		faces, err := eng.Detect(img)
		if err != nil {
			t.Fatalf("detect %s: %v", name, err)
		}
		if len(faces) == 0 {
			t.Fatalf("no face in %s", name)
		}
		f := faces[0]
		b := img.Bounds()
		if f.Box[0] < 0 || f.Box[2] > float32(b.Dx()) || f.Box[1] < 0 || f.Box[3] > float32(b.Dy()) {
			t.Fatalf("%s: box %v outside %v", name, f.Box, b)
		}
		emb, err := eng.Embed(img, f)
		if err != nil {
			t.Fatalf("embed %s: %v", name, err)
		}
		if len(emb) != 512 {
			t.Fatalf("dims: %d", len(emb))
		}
		return emb
	}
	o1 := embed("obama.jpg")
	o2 := embed("obama2.jpg")
	bi := embed("biden.jpg")
	same := CosineSimilarity(o1, o2)
	diff := CosineSimilarity(o1, bi)
	t.Logf("obama/obama2=%.3f obama/biden=%.3f", same, diff)
	if same < BuiltinProfiles["buffalo_s"].Threshold {
		t.Fatalf("same person below threshold: %.3f", same)
	}
	if diff >= BuiltinProfiles["buffalo_s"].Threshold {
		t.Fatalf("different persons above threshold: %.3f", diff)
	}
}
