package resource

import (
	"bytes"
	"strings"
	"testing"
)

func calibratedProvider() map[string]any {
	return map[string]any{"status": map[string]any{"calibration": map[string]any{
		"phase": "Calibrated", "message": "measured 2 of 2 models", "engineVersion": "0.34.4",
		"completedAt": "2026-10-17T03:37:10Z",
		"models": []any{
			map[string]any{"model": "qwen3:8b", "fits": true, "loadMs": int64(3119), "residentMiB": int64(9475),
				"generateTokensPerSecond": int64(152), "promptTokensPerSecond": int64(11748),
				"parallelRequests": int64(2), "parallelGenerateTokensPerSecond": int64(241)},
			map[string]any{"model": "qwen3:32b", "fits": false, "loadMs": int64(29527),
				"reason": "spills to the CPU: 22265 of 27771 MiB on the accelerator"},
			"not a model",
			map[string]any{"fits": true},
		},
	}}}
}

func TestRenderCalibration(t *testing.T) {
	var b bytes.Buffer
	if err := RenderCalibration(&b, calibratedProvider()); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	for _, want := range []string{
		"Calibration: Calibrated, measured 2 of 2 models, engine 0.34.4, completed 2026-10-17T03:37:10Z",
		"MODEL", "qwen3:8b", "3.1s", "152 tok/s", "11748 tok/s", "2 x 241 tok/s", "9475 MiB",
		"qwen3:32b", "29.5s", "spills to the CPU",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "qwen3:32b") > strings.Index(got, "qwen3:8b") {
		t.Errorf("rows are not sorted by model:\n%s", got)
	}
	if strings.Count(got, "\n") != 6 { // blank, summary, blank, header, two rows
		t.Errorf("malformed entries should be skipped:\n%s", got)
	}
}

func TestRenderCalibrationWithoutResults(t *testing.T) {
	var b bytes.Buffer
	if err := RenderCalibration(&b, map[string]any{"status": map[string]any{}}); err != nil || b.Len() != 0 {
		t.Errorf("an uncalibrated provider printed %q, err %v", b.String(), err)
	}
	starting := map[string]any{"status": map[string]any{"calibration": map[string]any{"phase": "Calibrating"}}}
	if err := RenderCalibration(&b, starting); err != nil || strings.TrimSpace(b.String()) != "Calibration: Calibrating" {
		t.Errorf("a calibration with no models printed %q, err %v", b.String(), err)
	}
	b.Reset()
	empty := map[string]any{"status": map[string]any{"calibration": map[string]any{}}}
	if err := RenderCalibration(&b, empty); err != nil || !strings.Contains(b.String(), "Calibration: "+none) {
		t.Errorf("a calibration with no phase printed %q, err %v", b.String(), err)
	}
}

func TestCalibrationRowReasons(t *testing.T) {
	noReason := calibrationRow(map[string]any{"model": "m", "fits": false})
	if noReason[len(noReason)-1] != "does not fit" {
		t.Errorf("a model that does not fit and has no reason: %v", noReason)
	}
	partly := calibrationRow(map[string]any{"model": "m", "fits": true, "reason": "parallel batch failed"})
	if partly[len(partly)-1] != "parallel batch failed" {
		t.Errorf("a model that fits keeps its reason: %v", partly)
	}
}

func TestRenderCalibrationWriteError(t *testing.T) {
	if err := RenderCalibration(failingWriter{}, calibratedProvider()); err == nil {
		t.Error("a write error must be returned")
	}
}

func TestModelProviderKindShowsCalibration(t *testing.T) {
	headers := map[string]string{}
	for _, c := range ModelProvider.Columns {
		headers[c.Header] = c.Path
	}
	if headers["CALIBRATION"] != "status.calibration.phase" {
		t.Errorf("modelprovider columns = %v", headers)
	}
	if ModelProvider.Details == nil {
		t.Error("a provider's get prints its calibration")
	}
}
