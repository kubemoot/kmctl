package resource

import (
	"fmt"
	"math"
	"time"
)

// stringField reads a string field, "" when absent or not a string.
func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// int64Field reads a whole-number field. The dynamic client decodes JSON integers
// as int64; an object built by hand or decoded by encoding/json may carry int or
// float64, so those are read too. A fractional or non-numeric value is absent.
func int64Field(m map[string]any, key string) (int64, bool) {
	switch v := m[key].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v == math.Trunc(v) && !math.IsInf(v, 0) {
			return int64(v), true
		}
	}
	return 0, false
}

// intField reads a whole-number field as text, "" when absent.
func intField(m map[string]any, key string) string {
	if v, ok := int64Field(m, key); ok {
		return fmt.Sprintf("%d", v)
	}
	return ""
}

// positiveInt reads a whole-number field that is above zero.
func positiveInt(m map[string]any, key string) (int64, bool) {
	v, ok := int64Field(m, key)
	return v, ok && v > 0
}

// boolField reads a boolean field and whether it is present as a boolean.
func boolField(m map[string]any, key string) (value, present bool) {
	value, present = m[key].(bool)
	return value, present
}

// formatMillis renders milliseconds as a duration rounded to round, at least the
// rounding unit so a short time never reads "0s".
func formatMillis(ms int64, round time.Duration) string {
	d := (time.Duration(ms) * time.Millisecond).Round(round)
	if d < round {
		d = round
	}
	return d.String()
}
