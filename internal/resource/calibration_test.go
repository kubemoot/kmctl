package resource

import (
	"bytes"
	"errors"
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
	cases := []struct {
		name string
		obj  map[string]any
		want string
	}{
		{"not calibrated", map[string]any{"status": map[string]any{}}, ""},
		{"no models yet", map[string]any{"status": map[string]any{"calibration": map[string]any{"phase": "Calibrating"}}},
			"Calibration: Calibrating"},
		{"no phase", map[string]any{"status": map[string]any{"calibration": map[string]any{}}}, "Calibration: " + none},
		{"unreadable", map[string]any{"status": map[string]any{"calibration": "garbled"}}, ""},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := RenderCalibration(&b, c.obj); err != nil || strings.TrimSpace(b.String()) != c.want {
			t.Errorf("%s: printed %q, err %v", c.name, b.String(), err)
		}
	}
}

// Every row keeps its column count, and unexpected values read as <none>, never a
// measured "no" or a wrong number.
func TestCalibrationRowUnexpectedInputs(t *testing.T) {
	cases := []struct {
		name string
		m    map[string]any
		want []string
	}{
		{"fits missing", map[string]any{"model": "m", "loadMs": int64(3000)},
			[]string{"m", none, "3s", none, none, none, none, ""}},
		{"fits not a bool", map[string]any{"model": "m", "fits": "yes"},
			[]string{"m", none, none, none, none, none, none, ""}},
		{"plain int and float64 numbers", map[string]any{"model": "m", "fits": true, "loadMs": 2500,
			"generateTokensPerSecond": float64(50), "residentMiB": int64(800)},
			[]string{"m", "yes", "2.5s", "50 tok/s", none, none, "800 MiB", ""}},
		{"zero, negative, and wrong-typed numbers", map[string]any{"model": "m", "fits": true, "loadMs": "3000",
			"generateTokensPerSecond": int64(0), "promptTokensPerSecond": int64(-4)},
			[]string{"m", "yes", none, none, none, none, none, ""}},
		{"parallel requests without their speed", map[string]any{"model": "m", "fits": true,
			"parallelRequests": int64(2)}, []string{"m", "yes", none, none, none, none, none, ""}},
		{"parallel speed without the request count", map[string]any{"model": "m", "fits": true,
			"parallelGenerateTokensPerSecond": int64(200)}, []string{"m", "yes", none, none, none, none, none, ""}},
		{"a very short load", map[string]any{"model": "m", "fits": true, "loadMs": int64(20)},
			[]string{"m", "yes", "100ms", none, none, none, none, ""}},
		{"does not fit, measured memory kept", map[string]any{"model": "m", "fits": false, "residentMiB": int64(27771)},
			[]string{"m", "no", none, none, none, none, "27771 MiB", "does not fit"}},
		{"fits, with a reason", map[string]any{"model": "m", "fits": true, "reason": "parallel batch failed"},
			[]string{"m", "yes", none, none, none, none, none, "parallel batch failed"}},
	}
	for _, c := range cases {
		if got := calibrationRow(c.m); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s:\n got %v\nwant %v", c.name, got, c.want)
		}
	}
}

// A writer that accepts the summary but fails on the table.
type failAfter struct{ left int }

func (f *failAfter) Write(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, errors.New("closed")
	}
	f.left--
	return len(p), nil
}

func TestRenderCalibrationWriteError(t *testing.T) {
	if err := RenderCalibration(failingWriter{}, calibratedProvider()); err == nil {
		t.Error("a write error on the summary must be returned")
	}
	if err := RenderCalibration(&failAfter{left: 2}, calibratedProvider()); err == nil {
		t.Error("a write error on the table must be returned")
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
