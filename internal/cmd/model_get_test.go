package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const calibratedServerJSON = `{"apiVersion":"kubemoot.ai/v1alpha1","kind":"ModelServer",
"metadata":{"name":"gpu-a","namespace":"team-a","creationTimestamp":"2026-10-01T10:00:00Z"},
"spec":{"type":"ollama","endpoint":"http://ollama:11434"},
"status":{"ready":true,"calibration":{"phase":"Calibrated","message":"measured 2 of 2 models","engineVersion":"0.34.4",
"models":[{"model":"qwen3:8b","fits":true,"loadMs":3119,"generateTokensPerSecond":152,"promptTokensPerSecond":11748,
"parallelRequests":2,"parallelGenerateTokensPerSecond":241,"residentMiB":9475},
{"model":"qwen3:32b","fits":false,"loadMs":29527,"reason":"spills to the CPU"}]}}}`

func TestModelServerGetPrintsCalibrationFromStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/kubemoot.ai/v1alpha1/namespaces/team-a/modelservers/gpu-a" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(calibratedServerJSON))
	}))
	t.Cleanup(srv.Close)

	cmd := newModelCommand(factoryFor(t, srv.URL))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"server", "get", "gpu-a"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("model server get: %v\n%s", err, out.String())
	}
	for _, want := range []string{"CALIBRATION", "Calibrated", "Calibration: Calibrated, measured 2 of 2 models, engine 0.34.4",
		"qwen3:8b", "152 tok/s", "2 x 241 tok/s", "qwen3:32b", "29.5s", "spills to the CPU"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}
