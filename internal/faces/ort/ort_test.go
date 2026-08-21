package ort_test

import (
	"os"
	"testing"

	"pocket-nas/internal/faces/ort"
)

// TestOpenMissingLibrary verifies graceful failure when the native library
// is absent (the default in CI and on user machines before download).
func TestOpenMissingLibrary(t *testing.T) {
	rt, err := ort.Open("/nonexistent/libonnxruntime.so")
	if err == nil {
		rt.Close()
		t.Fatal("expected error for missing library")
	}
}

// TestRealRuntime runs against a real onnxruntime build when
// POCKETNAS_ORT_LIB (and optionally POCKETNAS_REC_MODEL) are set — used by
// the M11 smoke script and CI's optional real-model step.
func TestRealRuntime(t *testing.T) {
	lib := os.Getenv("POCKETNAS_ORT_LIB")
	model := os.Getenv("POCKETNAS_REC_MODEL")
	if lib == "" || model == "" {
		t.Skip("set POCKETNAS_ORT_LIB and POCKETNAS_REC_MODEL to run")
	}
	rt, err := ort.Open(lib)
	if err != nil {
		t.Fatalf("open %s: %v", lib, err)
	}
	defer rt.Close()
	if v := rt.Version(); v == "" {
		t.Fatal("empty runtime version")
	}
	s, err := rt.NewSession(model, 2)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer s.Close()
	if len(s.Inputs) == 0 || len(s.Outputs) == 0 {
		t.Fatalf("no io names: in=%v out=%v", s.Inputs, s.Outputs)
	}
	in := ort.Tensor{Data: make([]float32, 1*3*112*112), Shape: []int64{1, 3, 112, 112}}
	out, err := s.Run(map[string]ort.Tensor{s.Inputs[0]: in}, s.Outputs)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	t1 := out[s.Outputs[0]]
	var flat int64 = 1
	for _, d := range t1.Shape {
		flat *= d
	}
	if int64(len(t1.Data)) != flat || flat == 0 {
		t.Fatalf("bad output shape %v (data %d)", t1.Shape, len(t1.Data))
	}
}
