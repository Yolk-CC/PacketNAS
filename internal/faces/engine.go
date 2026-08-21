// Package faces implements SPEC-M11 server-side face recognition:
// detection + embedding via pluggable ONNX profiles, clustering into
// persons, a migration-friendly store and the /api/faces HTTP API.
//
// All native inference goes through the ort package which dlopens
// libonnxruntime at runtime; when the library or models are missing the
// whole feature degrades gracefully (available=false) without affecting
// the rest of the server.
package faces

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"sort"
	"sync"

	"github.com/disintegration/imaging"

	"pocket-nas/internal/faces/ort"
)

// Face is one detected face in original-image pixel coordinates.
type Face struct {
	Box       [4]float32    `json:"box"`       // x1,y1,x2,y2
	Landmarks [5][2]float32 `json:"landmarks"` // eyes, nose, mouth corners
	Score     float32       `json:"score"`
}

// Engine abstracts detection + embedding so model swaps only need config.
type Engine interface {
	// Detect finds faces in img (EXIF-orientation already applied by the
	// caller's decode path, aligned with thumbnail generation).
	Detect(img image.Image) ([]Face, error)
	// Embed aligns face f by its 5 landmarks and returns the L2-normalized
	// embedding (length = profile Dims).
	Embed(img image.Image, f Face) ([]float32, error)
	// Dims reports the embedding dimensionality.
	Dims() int
	// ProfileName identifies the profile the engine was built from.
	ProfileName() string
}

// Profile bundles everything a model family needs: file names, input
// geometry and thresholds. Adding a new model = adding a profile.
type Profile struct {
	Name            string  `json:"name"`
	DetModel        string  `json:"detModel"`  // file name inside models dir
	RecModel        string  `json:"recModel"`  // file name inside models dir
	DetSize         int     `json:"detSize"`   // SCRFD max side (letterbox target)
	RecSize         int     `json:"recSize"`   // alignment crop size (112 for ArcFace)
	Dims            int     `json:"dims"`      // embedding dimensions
	Threshold       float32 `json:"threshold"` // cosine-similarity clustering threshold
	ScoreThreshold  float32 `json:"scoreThreshold"`
	NMSThreshold    float32 `json:"nmsThreshold"`
	RecInputName    string  `json:"recInputName"`  // "" → first session input
	RecOutputName   string  `json:"recOutputName"` // "" → first session output
	DetThreads      int     `json:"detThreads"`    // intra-op threads, 0 = default
	RecNormalizeStd float32 `json:"-"`             // fixed 127.5 for insightface family
}

// BuiltinProfiles: buffalo_l (accuracy), buffalo_s (balanced),
// mobilefacenet (lightest). All insightface-family, so preprocessing and
// alignment are shared; only file names / dims / thresholds differ.
var BuiltinProfiles = map[string]Profile{
	"buffalo_l": {
		Name: "buffalo_l", DetModel: "det_10g.onnx", RecModel: "w600k_r50.onnx",
		DetSize: 640, RecSize: 112, Dims: 512,
		Threshold: 0.5, ScoreThreshold: 0.5, NMSThreshold: 0.4,
	},
	"buffalo_s": {
		Name: "buffalo_s", DetModel: "det_500m.onnx", RecModel: "w600k_mbf.onnx",
		DetSize: 640, RecSize: 112, Dims: 512,
		Threshold: 0.5, ScoreThreshold: 0.5, NMSThreshold: 0.4,
	},
	"mobilefacenet": {
		Name: "mobilefacenet", DetModel: "det_500m.onnx", RecModel: "mobilefacenet.onnx",
		DetSize: 640, RecSize: 112, Dims: 128,
		Threshold: 0.45, ScoreThreshold: 0.5, NMSThreshold: 0.4,
	},
}

// DefaultProfile is used when the user never chose one.
const DefaultProfile = "buffalo_s"

// OnnxEngine implements Engine on top of ort.Runtime with a detector and a
// recognition session. Construct via NewOnnxEngine.
type OnnxEngine struct {
	rt   *ort.Runtime
	det  *ort.Session
	rec  *ort.Session
	prof Profile
	mu   sync.Mutex // ORT sessions are not documented thread-safe; serialize
}

// NewOnnxEngine opens libPath (onnxruntime shared library) and loads the
// profile's det/rec models from modelsDir. Any failure is returned as an
// error for graceful degradation.
func NewOnnxEngine(libPath, modelsDir string, prof Profile) (*OnnxEngine, error) {
	if prof.RecSize == 0 {
		prof.RecSize = 112
	}
	if prof.DetSize == 0 {
		prof.DetSize = 640
	}
	if prof.NMSThreshold == 0 {
		prof.NMSThreshold = 0.4
	}
	if prof.ScoreThreshold == 0 {
		prof.ScoreThreshold = 0.5
	}
	rt, err := ort.Open(libPath)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*OnnxEngine, error) {
		rt.Close()
		return nil, err
	}
	det, err := rt.NewSession(filepath.Join(modelsDir, prof.DetModel), prof.DetThreads)
	if err != nil {
		return fail(fmt.Errorf("detector %s: %w", prof.DetModel, err))
	}
	rec, err := rt.NewSession(filepath.Join(modelsDir, prof.RecModel), 1)
	if err != nil {
		det.Close()
		return fail(fmt.Errorf("recognizer %s: %w", prof.RecModel, err))
	}
	return &OnnxEngine{rt: rt, det: det, rec: rec, prof: prof}, nil
}

// Close releases sessions and the runtime.
func (e *OnnxEngine) Close() {
	if e == nil {
		return
	}
	if e.det != nil {
		e.det.Close()
	}
	if e.rec != nil {
		e.rec.Close()
	}
	if e.rt != nil {
		e.rt.Close()
	}
}

// Dims implements Engine.
func (e *OnnxEngine) Dims() int { return e.prof.Dims }

// ProfileName implements Engine.
func (e *OnnxEngine) ProfileName() string { return e.prof.Name }

// scrfdOut groups the 9 SCRFD outputs by kind. InsightFace SCRFD models
// emit (in model-defined order) 3 score maps, 3 bbox maps, 3 kps maps, but
// the output NAMES are opaque ("443", "score_8", ...), so we classify by
// trailing dimension: 1=score, 4=bbox, 10=kps, and sort by leading dim
// descending (stride 8, 16, 32).
type scrfdOut struct {
	scores []ort.Tensor // per stride
	boxes  []ort.Tensor
	kps    []ort.Tensor
}

func classifyDetOutputs(out map[string]ort.Tensor) (scrfdOut, error) {
	var res scrfdOut
	for _, t := range out {
		if len(t.Shape) != 2 {
			return res, fmt.Errorf("unexpected det output rank %d", len(t.Shape))
		}
		switch t.Shape[1] {
		case 1:
			res.scores = append(res.scores, t)
		case 4:
			res.boxes = append(res.boxes, t)
		case 10:
			res.kps = append(res.kps, t)
		}
	}
	byFirst := func(s []ort.Tensor) {
		sort.Slice(s, func(i, j int) bool { return s[i].Shape[0] > s[j].Shape[0] })
	}
	byFirst(res.scores)
	byFirst(res.boxes)
	byFirst(res.kps)
	if len(res.scores) == 0 || len(res.scores) != len(res.boxes) {
		return res, fmt.Errorf("not a SCRFD-style detector (scores=%d boxes=%d)", len(res.scores), len(res.boxes))
	}
	return res, nil
}

var scrfdStrides = []int{8, 16, 32}

// Detect implements Engine: letterbox to DetSize, run SCRFD, decode
// anchors, NMS, map back to original coordinates.
func (e *OnnxEngine) Detect(img image.Image) ([]Face, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("empty image")
	}
	scale := float32(e.prof.DetSize) / float32(max(w, h))
	if scale > 1 {
		scale = 1
	}
	nw, nh := int(float32(w)*scale+0.5), int(float32(h)*scale+0.5)
	// Pad to a multiple of 32 so all FPN strides divide evenly.
	pw, ph := roundUp(nw, 32), roundUp(nh, 32)
	resized := imaging.Resize(img, nw, nh, imaging.Linear)
	canvas := imaging.New(pw, ph, image.Black)
	canvas = imaging.Paste(canvas, resized, image.Pt(0, 0))

	input := nchwNormalize(canvas, 127.5, 127.5)
	out, err := e.det.Run(
		map[string]ort.Tensor{e.det.Inputs[0]: {Data: input, Shape: []int64{1, 3, int64(ph), int64(pw)}}},
		e.det.Outputs)
	if err != nil {
		return nil, err
	}
	so, err := classifyDetOutputs(out)
	if err != nil {
		return nil, err
	}
	var faces []Face
	for level, stride := range scrfdStrides {
		if level >= len(so.scores) {
			break
		}
		faces = append(faces, decodeLevel(so.scores[level], so.boxes[level], kpsAt(so.kps, level),
			stride, ph, pw, e.prof.ScoreThreshold)...)
	}
	faces = nms(faces, e.prof.NMSThreshold)
	// Map letterboxed coords back to the original image.
	for i := range faces {
		for k := 0; k < 4; k += 2 {
			faces[i].Box[k] /= scale
			faces[i].Box[k+1] /= scale
		}
		for k := range faces[i].Landmarks {
			faces[i].Landmarks[k][0] /= scale
			faces[i].Landmarks[k][1] /= scale
		}
	}
	return faces, nil
}

func kpsAt(kps []ort.Tensor, i int) *ort.Tensor {
	if i < len(kps) {
		return &kps[i]
	}
	return nil
}

func roundUp(n, m int) int { return (n + m - 1) / m * m }

// nchwNormalize converts img to planar NCHW float32: (v-mean)/std, RGB.
func nchwNormalize(img *image.NRGBA, mean, std float32) []float32 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]float32, 3*w*h)
	plane := w * h
	for y := 0; y < h; y++ {
		row := y * img.Stride
		for x := 0; x < w; x++ {
			o := row + x*4
			i := y*w + x
			out[i] = (float32(img.Pix[o]) - mean) / std
			out[plane+i] = (float32(img.Pix[o+1]) - mean) / std
			out[2*plane+i] = (float32(img.Pix[o+2]) - mean) / std
		}
	}
	return out
}

// decodeLevel decodes one FPN level of SCRFD outputs.
func decodeLevel(scores, boxes ort.Tensor, kps *ort.Tensor, stride, ph, pw int, thresh float32) []Face {
	fh, fw := ph/stride, pw/stride
	const anchors = 2
	var out []Face
	for idx, sc := range scores.Data {
		if sc < thresh {
			continue
		}
		anchor := idx % anchors
		_ = anchor // anchors share the same decoding; index not needed
		cell := idx / anchors
		cy, cx := cell/fw, cell%fw
		px := float32(cx * stride)
		py := float32(cy * stride)
		d := boxes.Data[idx*4 : idx*4+4]
		f := Face{
			Box: [4]float32{
				px - d[0]*float32(stride),
				py - d[1]*float32(stride),
				px + d[2]*float32(stride),
				py + d[3]*float32(stride),
			},
			Score: sc,
		}
		if kps != nil {
			k := kps.Data[idx*10 : idx*10+10]
			for j := 0; j < 5; j++ {
				f.Landmarks[j][0] = px + k[j*2]*float32(stride)
				f.Landmarks[j][1] = py + k[j*2+1]*float32(stride)
			}
		}
		out = append(out, f)
	}
	_ = fh
	return out
}

// nms is greedy IoU suppression, score-descending.
func nms(faces []Face, thresh float32) []Face {
	sort.Slice(faces, func(i, j int) bool { return faces[i].Score > faces[j].Score })
	keep := make([]Face, 0, len(faces))
	suppressed := make([]bool, len(faces))
	for i, f := range faces {
		if suppressed[i] {
			continue
		}
		keep = append(keep, f)
		for j := i + 1; j < len(faces); j++ {
			if suppressed[j] {
				continue
			}
			if iou(f.Box, faces[j].Box) > thresh {
				suppressed[j] = true
			}
		}
	}
	return keep
}

func iou(a, b [4]float32) float32 {
	x1 := float32(math.Max(float64(a[0]), float64(b[0])))
	y1 := float32(math.Max(float64(a[1]), float64(b[1])))
	x2 := float32(math.Min(float64(a[2]), float64(b[2])))
	y2 := float32(math.Min(float64(a[3]), float64(b[3])))
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	inter := (x2 - x1) * (y2 - y1)
	area := func(bx [4]float32) float32 { return (bx[2] - bx[0]) * (bx[3] - bx[1]) }
	return inter / (area(a) + area(b) - inter)
}

// arcFaceTemplate is the standard 112x112 destination landmarks.
var arcFaceTemplate = [5][2]float32{
	{38.2946, 51.6963}, {73.5318, 51.5014}, {56.0252, 71.7366},
	{41.5493, 92.3655}, {70.7299, 92.2041},
}

// Embed aligns the face to RecSize×RecSize via a similarity transform and
// runs the recognition model. Output is L2-normalized.
func (e *OnnxEngine) Embed(img image.Image, f Face) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	size := e.prof.RecSize
	tpl := arcFaceTemplate
	if size != 112 {
		s := float32(size) / 112
		for i := range tpl {
			tpl[i][0] *= s
			tpl[i][1] *= s
		}
	}
	m := estimateSimilarity(f.Landmarks, tpl)
	crop := warpAffine(img, m, size)
	input := nchwNormalize(crop, 127.5, 127.5)
	inName := e.prof.RecInputName
	if inName == "" {
		inName = e.rec.Inputs[0]
	}
	outName := e.prof.RecOutputName
	if outName == "" {
		outName = e.rec.Outputs[0]
	}
	out, err := e.rec.Run(
		map[string]ort.Tensor{inName: {Data: input, Shape: []int64{1, 3, int64(size), int64(size)}}},
		[]string{outName})
	if err != nil {
		return nil, err
	}
	t := out[outName]
	if len(t.Data) != e.prof.Dims {
		return nil, fmt.Errorf("recognizer produced %d dims, profile expects %d", len(t.Data), e.prof.Dims)
	}
	return l2normalize(t.Data), nil
}

// l2normalize returns v scaled to unit length (zero-safe).
func l2normalize(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	n := float32(math.Sqrt(s))
	if n < 1e-12 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / n
	}
	return out
}

// similarity is a 2D similarity transform: dst ≈ s·R·src + t.
type similarity struct{ a, b, tx, ty float64 } // [a -b tx; b a ty]

// estimateSimilarity solves the least-squares similarity transform from src
// to dst (umeyama, 2D, without reflection).
func estimateSimilarity(src, dst [5][2]float32) similarity {
	var mx1, my1, mx2, my2 float64
	for i := 0; i < 5; i++ {
		mx1 += float64(src[i][0])
		my1 += float64(src[i][1])
		mx2 += float64(dst[i][0])
		my2 += float64(dst[i][1])
	}
	mx1, my1, mx2, my2 = mx1/5, my1/5, mx2/5, my2/5
	var sxx, sxy, syy, dxx, dxy float64
	for i := 0; i < 5; i++ {
		x1 := float64(src[i][0]) - mx1
		y1 := float64(src[i][1]) - my1
		x2 := float64(dst[i][0]) - mx2
		y2 := float64(dst[i][1]) - my2
		sxx += x1 * x1
		syy += y1 * y1
		sxy += x1 * y1
		dxx += x1*x2 + y1*y2 // cov components for similarity
		dxy += x1*y2 - y1*x2
	}
	var norm = sxx + syy
	if norm < 1e-12 {
		norm = 1
	}
	a := dxx / norm
	b := dxy / norm
	return similarity{
		a:  a,
		b:  b,
		tx: mx2 - (a*mx1 - b*my1),
		ty: my2 - (b*mx1 + a*my1),
	}
}

// warpAffine samples src into an NRGBA of size×size using m (forward map
// src→dst), with bilinear interpolation; out-of-range pixels stay black.
func warpAffine(src image.Image, m similarity, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	sb := src.Bounds()
	// Invert the similarity transform for dst→src sampling.
	det := m.a*m.a + m.b*m.b
	if det < 1e-12 {
		return dst
	}
	ia, ib := m.a/det, -m.b/det
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx := float64(x) - m.tx
			fy := float64(y) - m.ty
			sx := ia*fx - ib*fy
			sy := ib*fx + ia*fy
			c := bilinear(src, sb, sx, sy)
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = c[0], c[1], c[2], 0xff
		}
	}
	return dst
}

func bilinear(img image.Image, b image.Rectangle, x, y float64) [3]byte {
	if x < 0 || y < 0 || x > float64(b.Dx()-1) || y > float64(b.Dy()-1) {
		return [3]byte{}
	}
	x0, y0 := int(x), int(y)
	x1, y1 := x0+1, y0+1
	if x1 >= b.Dx() {
		x1 = b.Dx() - 1
	}
	if y1 >= b.Dy() {
		y1 = b.Dy() - 1
	}
	fx, fy := x-float64(x0), y-float64(y0)
	px := func(px, py int) (float64, float64, float64) {
		r, g, bl, _ := img.At(b.Min.X+px, b.Min.Y+py).RGBA()
		return float64(r >> 8), float64(g >> 8), float64(bl >> 8)
	}
	c00r, c00g, c00b := px(x0, y0)
	c10r, c10g, c10b := px(x1, y0)
	c01r, c01g, c01b := px(x0, y1)
	c11r, c11g, c11b := px(x1, y1)
	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }
	out := [3]byte{}
	cr := lerp(lerp(c00r, c10r, fx), lerp(c01r, c11r, fx), fy)
	cg := lerp(lerp(c00g, c10g, fx), lerp(c01g, c11g, fx), fy)
	cb := lerp(lerp(c00b, c10b, fx), lerp(c01b, c11b, fx), fy)
	out[0], out[1], out[2] = byte(cr+0.5), byte(cg+0.5), byte(cb+0.5)
	return out
}
