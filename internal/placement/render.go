package placement

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// NoCallsNote is printed for a receipt that carries no placement calls.
const NoCallsNote = "This turn's receipt carries no placement calls (agents older than this feature do not report them)."

// NoReceiptNote is printed when the turn closed without a receipt.
const NoReceiptNote = "This turn closed without a receipt (the crew's coordinator or discussion gateway is older than this feature)."

// Render writes the table of calls (one row each), then each call's reason and the
// alternatives it weighed, then a summary line.
func Render(w io.Writer, calls []Call) error {
	if len(calls) == 0 {
		_, err := fmt.Fprintln(w, NoCallsNote)
		return err
	}
	if err := table(w, calls); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if err := details(w, calls); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%s\n", SummaryLine(Summarize(calls)))
	return err
}

func table(w io.Writer, calls []Call) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "#\tAGENT\tPHASE\tPLACEMENT\tPOLICY\tPREDICTED\tACTUAL\tERROR")
	for i, c := range calls {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", i+1,
			dash(c.Agent), dash(c.Phase), c.Place()+coldMark(c), policyName(c.Policy),
			predictedText(c.Predicted), actualText(c.Actual), errorText(c.Error))
	}
	return tw.Flush()
}

// details lists, per call, why it was placed and the servers weighed against it.
func details(w io.Writer, calls []Call) error {
	for i, c := range calls {
		if _, err := fmt.Fprintf(w, "[%d] %s %s on %s\n", i+1, dash(c.Agent), dash(c.Phase), c.Place()); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "    reason: %s\n", orNone(c.Reason)); err != nil {
			return err
		}
		if err := firstToken(w, c); err != nil {
			return err
		}
		if err := alternatives(w, c.Alternatives); err != nil {
			return err
		}
	}
	return nil
}

// firstToken writes the predicted and measured time to the first token, when the
// receipt has either.
func firstToken(w io.Writer, c Call) error {
	var predicted, actual int64
	if c.Predicted != nil {
		predicted = c.Predicted.FirstTokenMs
	}
	if c.Actual != nil {
		actual = c.Actual.FirstTokenMs
	}
	if predicted == 0 && actual == 0 {
		return nil
	}
	_, err := fmt.Fprintf(w, "    first token: predicted %s, measured %s\n", msOrDash(predicted), msOrDash(actual))
	return err
}

func msOrDash(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return seconds(ms)
}

func alternatives(w io.Writer, options []Option) error {
	if len(options) == 0 {
		_, err := fmt.Fprintln(w, "    alternatives: none weighed")
		return err
	}
	if _, err := fmt.Fprintln(w, "    alternatives:"); err != nil {
		return err
	}
	for _, o := range options {
		if _, err := fmt.Fprintf(w, "      %s  predicted %s  (%s)\n", optionName(o), predictedText(o.Predicted), optionState(o)); err != nil {
			return err
		}
	}
	return nil
}

func optionName(o Option) string {
	if o.Model != "" {
		return o.Model + "@" + o.Server
	}
	return o.Server
}

func optionState(o Option) string {
	states := []string{"cold", "waits for a slot"}
	if o.Warm {
		states[0] = "warm"
	}
	if o.StartsNow {
		states[1] = "starts now"
	}
	return strings.Join(states, ", ")
}

// SummaryLine is the calls per policy and the mean absolute error.
func SummaryLine(s Stats) string {
	policies := make([]string, 0, len(s.ByPolicy))
	for p := range s.ByPolicy {
		policies = append(policies, p)
	}
	sort.Strings(policies)
	parts := make([]string, len(policies))
	for i, p := range policies {
		parts[i] = fmt.Sprintf("%d %s", s.ByPolicy[p], p)
	}
	line := fmt.Sprintf("%d %s: %s.", s.Calls, plural(s.Calls, "call", "calls"), strings.Join(parts, ", "))
	if s.Measured == 0 {
		return line + " No call has a measured error yet."
	}
	return line + fmt.Sprintf(" Mean absolute error %s over the %d %s with a measured error.",
		seconds(int64(s.MeanAbsErrorMs+0.5)), s.Measured, plural(s.Measured, "call", "calls"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func predictedText(p *Prediction) string {
	if p == nil {
		return "-"
	}
	return seconds(p.TotalMs)
}

// actualText is the measured total; a request that failed says so.
func actualText(a *Actual) string {
	switch {
	case a == nil:
		return "-"
	case !a.OK:
		return "failed"
	}
	return seconds(a.TotalMs)
}

// errorText is the signed error (actual minus predicted) with its relative size.
func errorText(e *Error) string {
	if e == nil {
		return "-"
	}
	sign := "+"
	if e.TotalSignedMs < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%s (%.0f%%)", sign, seconds(abs(e.TotalSignedMs)), e.TotalRelative*100)
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// seconds is a millisecond count as seconds, or as milliseconds under one second.
func seconds(ms int64) string {
	if ms < 1000 && ms > -1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func coldMark(c Call) string {
	if c.ColdStart {
		return " (cold)"
	}
	return ""
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return "none given"
	}
	return s
}
