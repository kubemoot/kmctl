package resource

import (
	"math"
	"testing"
	"time"
)

func TestInt64FieldReadsEveryWholeNumberType(t *testing.T) {
	m := map[string]any{"a": int64(7), "b": 7, "c": float64(7), "frac": 7.5, "inf": math.Inf(1), "s": "7", "nil": nil}
	for _, key := range []string{"a", "b", "c"} {
		if v, ok := int64Field(m, key); !ok || v != 7 {
			t.Errorf("%s: %d %v", key, v, ok)
		}
	}
	for _, key := range []string{"frac", "inf", "s", "nil", "missing"} {
		if _, ok := int64Field(m, key); ok {
			t.Errorf("%s should be absent", key)
		}
	}
	if _, ok := positiveInt(map[string]any{"z": int64(0), "n": int64(-3)}, "z"); ok {
		t.Error("zero is not positive")
	}
	if _, ok := positiveInt(map[string]any{"n": int64(-3)}, "n"); ok {
		t.Error("a negative number is not positive")
	}
}

func TestBoolField(t *testing.T) {
	m := map[string]any{"t": true, "f": false, "s": "true"}
	if v, ok := boolField(m, "t"); !v || !ok {
		t.Error("true")
	}
	if v, ok := boolField(m, "f"); v || !ok {
		t.Error("false is present")
	}
	for _, key := range []string{"s", "missing"} {
		if _, ok := boolField(m, key); ok {
			t.Errorf("%s should be absent", key)
		}
	}
}

func TestFormatMillis(t *testing.T) {
	cases := []struct {
		ms    int64
		round time.Duration
		want  string
	}{
		{3119, 100 * time.Millisecond, "3.1s"},
		{20, 100 * time.Millisecond, "100ms"},
		{2000, time.Second, "2s"},
		{400, time.Second, "1s"},
	}
	for _, c := range cases {
		if got := formatMillis(c.ms, c.round); got != c.want {
			t.Errorf("formatMillis(%d, %s) = %s, want %s", c.ms, c.round, got, c.want)
		}
	}
}
