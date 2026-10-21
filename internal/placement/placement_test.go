package placement

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/receipt.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecode_SampleReceipt(t *testing.T) {
	d, err := Decode(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Calls) != 3 || len(d.Raw) != 3 {
		t.Fatalf("want 3 calls, got %d decoded and %d raw", len(d.Calls), len(d.Raw))
	}
	assertFirstCall(t, d.Calls[0])
	if shadow := d.Calls[2]; shadow.Predicted != nil || shadow.Error != nil || shadow.Actual == nil {
		t.Errorf("a call without a prediction has an actual only: %+v", shadow)
	}
}

func assertFirstCall(t *testing.T, c Call) {
	t.Helper()
	if c.Place() != "qwen3:14b@ollama-gpu" || c.Policy != "learned" || len(c.Alternatives) != 1 {
		t.Errorf("first call decoded wrong: %+v", c)
	}
	if c.Predicted.TotalMs != 2900 || c.Actual.FirstTokenMs != 700 || c.Error.TotalSignedMs != 500 {
		t.Errorf("first call numbers decoded wrong: %+v", c)
	}
}

func TestDecode_NoCalls(t *testing.T) {
	for name, in := range map[string]string{
		"empty":       "",
		"no calls":    `{"totalMs": 5}`,
		"empty array": `{"calls": []}`,
		"null":        `{"calls": null}`,
	} {
		d, err := Decode(json.RawMessage(in))
		if err != nil || len(d.Calls) != 0 {
			t.Errorf("%s: got %+v, %v; want no calls and no error", name, d, err)
		}
	}
}

func TestDecode_Malformed(t *testing.T) {
	for name, in := range map[string]string{
		"not json":        `{`,
		"calls a string":  `{"calls": "x"}`,
		"call not object": `{"calls": [1]}`,
		"field wrong":     `{"calls": [{"model": 3}]}`,
	} {
		if _, err := Decode(json.RawMessage(in)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestDecode_MissingFieldsAndZeroActual(t *testing.T) {
	d, err := Decode(json.RawMessage(`{"calls":[{"model":"m","server":"s","policy":"algorithm","actual":{"ok":false,"totalMs":0,"inputTokens":0,"outputTokens":0,"requests":0}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, d.Calls); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"m@s", "algorithm", "failed", "reason: none given", "alternatives: none weighed", "No call has a measured error yet."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDecode_NullElementIsAnError(t *testing.T) {
	_, err := Decode(json.RawMessage(`{"calls":[{"model":"m","server":"s","policy":"learned"}, null]}`))
	if err == nil || !strings.Contains(err.Error(), "call 2") {
		t.Errorf("a null call: %v", err)
	}
}

func TestRender_UnknownPolicyAndFirstToken(t *testing.T) {
	d, err := Decode(json.RawMessage(`{"calls":[{"model":"m","server":"s","policy":"future-policy",` +
		`"predicted":{"waitMs":0,"loadMs":0,"promptMs":500,"firstTokenMs":500,"generateMs":1000,"totalMs":1500},` +
		`"actual":{"ok":true,"firstTokenMs":900,"totalMs":1800,"inputTokens":1,"outputTokens":1,"requests":1}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, d.Calls); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"future-policy", "1 call: 1 future-policy.", "first token: predicted 500ms, measured 900ms"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestRender_SampleKeepsReceiptOrderAndFirstToken(t *testing.T) {
	d, _ := Decode(fixture(t))
	var buf bytes.Buffer
	_ = Render(&buf, d.Calls)
	out := buf.String()
	if strings.Index(out, "kubernetes-specialist   evaluation") > strings.Index(out, "obs-metrics   ") {
		t.Errorf("rows are in the receipt's order:\n%s", out)
	}
	if !strings.Contains(out, "first token: predicted 600ms, measured 700ms") {
		t.Errorf("first-token line missing:\n%s", out)
	}
	if strings.Contains(out, "first token: predicted -, measured -") {
		t.Error("a call with no first-token figures prints no first-token line")
	}
}

func TestSummarize(t *testing.T) {
	d, _ := Decode(fixture(t))
	s := Summarize(d.Calls)
	if s.Calls != 3 || s.ByPolicy["learned"] != 1 || s.ByPolicy["algorithm"] != 1 || s.ByPolicy["shadow"] != 1 {
		t.Errorf("policy counts: %+v", s)
	}
	if s.Measured != 2 || s.MeanAbsErrorMs != 1450 {
		t.Errorf("error stats: %+v", s)
	}
}

func TestSummarize_EmptyAndUnknownPolicy(t *testing.T) {
	if empty := Summarize(nil); empty.Calls != 0 || empty.MeanAbsErrorMs != 0 {
		t.Errorf("empty: %+v", empty)
	}
	if u := Summarize([]Call{{}}); u.ByPolicy["unknown"] != 1 {
		t.Errorf("a call without a policy counts as unknown: %+v", u)
	}
}

func TestRender_SampleReceipt(t *testing.T) {
	d, _ := Decode(fixture(t))
	var buf bytes.Buffer
	if err := Render(&buf, d.Calls); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"AGENT", "PHASE", "PLACEMENT", "POLICY", "PREDICTED", "ACTUAL", "ERROR",
		"coordinator", "qwen3:14b@ollama-gpu", "2.9s", "3.4s", "+500ms (15%)",
		"kubernetes-specialist", "qwen3:14b@ollama-gpu (cold)", "11.4s", "13.8s", "+2.4s (17%)",
		"obs-metrics", "shadow",
		"reason: cold-load fits 14000 <= 32605",
		"ollama-cpu  predicted 61.0s  (warm, starts now)",
		"learned would choose qwen3:8b@ollama-gpu",
		"3 calls: 1 algorithm, 1 learned, 1 shadow. Mean absolute error 1.4s over the 2 calls with a measured error.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "\u2013\u2014") {
		t.Error("output has a non-ASCII dash")
	}
}

func TestRender_NoCalls(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, nil); err != nil || buf.String() != NoCallsNote+"\n" {
		t.Errorf("got %q, %v", buf.String(), err)
	}
}

func TestErrorText_SignAndOptions(t *testing.T) {
	if got := errorText(&Error{TotalSignedMs: -1200, TotalRelative: 0.25}); got != "-1.2s (25%)" {
		t.Errorf("faster than predicted: %q", got)
	}
	if got := errorText(nil); got != "-" {
		t.Errorf("no error: %q", got)
	}
	if got := optionName(Option{Server: "s", Model: "m"}); got != "m@s" {
		t.Errorf("option with its own model: %q", got)
	}
	if got := optionState(Option{}); got != "cold, waits for a slot" {
		t.Errorf("cold busy option: %q", got)
	}
	if got := SummaryLine(Stats{Calls: 1, ByPolicy: map[string]int{"learned": 1}, Measured: 1, MeanAbsErrorMs: 400}); !strings.Contains(got, "1 call:") || !strings.Contains(got, "400ms over the 1 call ") {
		t.Errorf("singular summary: %q", got)
	}
}
