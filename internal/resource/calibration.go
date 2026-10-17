package resource

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/output"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CalibrationPaths are the ModelProvider status fields RenderCalibration reads;
// add a path here with every field it starts using. The Tier 2 contract test
// checks each one against the CRD schema.
var CalibrationPaths = []string{
	"status.calibration.phase",
	"status.calibration.message",
	"status.calibration.engineVersion",
	"status.calibration.completedAt",
	"status.calibration.models.model",
	"status.calibration.models.fits",
	"status.calibration.models.reason",
	"status.calibration.models.loadMs",
	"status.calibration.models.residentMiB",
	"status.calibration.models.generateTokensPerSecond",
	"status.calibration.models.promptTokensPerSecond",
	"status.calibration.models.parallelRequests",
	"status.calibration.models.parallelGenerateTokensPerSecond",
}

// RenderCalibration writes what calibration recorded on a ModelProvider after its
// summary row: the phase line, then one row per model. It writes nothing when the
// provider has not been calibrated.
func RenderCalibration(w io.Writer, obj map[string]any) error {
	cal, ok, _ := unstructured.NestedMap(obj, "status", "calibration")
	if !ok {
		return nil
	}
	out := output.NewWriter(w)
	out.Printf("\nCalibration: %s\n", calibrationSummary(cal))
	rows := calibrationRows(cal)
	if out.Err() != nil || len(rows) == 0 {
		return out.Err()
	}
	out.Printf("\n")
	if out.Err() != nil {
		return out.Err()
	}
	return writeTable(w, []string{"MODEL", "FITS", "LOAD", "GENERATE", "PROMPT", "PARALLEL", "MEMORY", "REASON"}, rows)
}

// calibrationSummary is the phase with its message, engine version, and completion
// time, each only when status has it.
func calibrationSummary(cal map[string]any) string {
	parts := []string{orNone(stringField(cal, "phase"))}
	for _, part := range []struct{ prefix, key string }{
		{"", "message"}, {"engine ", "engineVersion"}, {"completed ", "completedAt"},
	} {
		if v := stringField(cal, part.key); v != "" {
			parts = append(parts, part.prefix+v)
		}
	}
	return strings.Join(parts, ", ")
}

// calibrationRows is one row per calibrated model, sorted by model name.
func calibrationRows(cal map[string]any) [][]string {
	items, _, _ := unstructured.NestedSlice(cal, "models")
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok || stringField(m, "model") == "" {
			continue
		}
		rows = append(rows, calibrationRow(m))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return rows
}

// calibrationRow is one model's measurements. A model that does not fit keeps what
// was measured before it was found not to (load time, memory) and its reason; a
// row without fits reads <none>, never a measured "no".
func calibrationRow(m map[string]any) []string {
	fits, known := boolField(m, "fits")
	row := []string{
		stringField(m, "model"), yesNo(fits, known), loadTime(m),
		tokensPerSecond(m, "generateTokensPerSecond"), tokensPerSecond(m, "promptTokensPerSecond"),
		parallelSpeed(m), mebibytes(m, "residentMiB"), stringField(m, "reason"),
	}
	if known && !fits && row[7] == "" {
		row[7] = "does not fit"
	}
	return row
}

func yesNo(value, known bool) string {
	switch {
	case !known:
		return none
	case value:
		return "yes"
	default:
		return "no"
	}
}

func loadTime(m map[string]any) string {
	ms, ok := positiveInt(m, "loadMs")
	if !ok {
		return none
	}
	return formatMillis(ms, 100*time.Millisecond)
}

func tokensPerSecond(m map[string]any, key string) string {
	v, ok := positiveInt(m, key)
	if !ok {
		return none
	}
	return fmt.Sprintf("%d tok/s", v)
}

func parallelSpeed(m map[string]any) string {
	n, okN := positiveInt(m, "parallelRequests")
	v, okV := positiveInt(m, "parallelGenerateTokensPerSecond")
	if !okN || !okV {
		return none
	}
	return fmt.Sprintf("%d x %d tok/s", n, v)
}

func mebibytes(m map[string]any, key string) string {
	v, ok := positiveInt(m, key)
	if !ok {
		return none
	}
	return fmt.Sprintf("%d MiB", v)
}
