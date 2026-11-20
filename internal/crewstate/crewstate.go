// Package crewstate reads what a Crew's status says about two separate questions:
// is the crew reconciled as declared, and can it answer a question now. They differ
// because a crew whose models are away stays reconciled (status.ready true) while it
// waits for capacity; only the CapacityAvailable condition and the WaitingForCapacity
// phase say it cannot answer yet.
//
// A Kubemoot that predates CapacityAvailable (0.95) reports neither the condition nor
// the phase; the readers fall back to status.ready and status.phase alone, which is
// what they meant on that Kubemoot.
package crewstate

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Condition and phase names the operator writes on a Crew.
const (
	ConditionCapacity  = "CapacityAvailable"
	ConditionRetention = "RetentionValid"

	PhaseReady   = "Ready"
	PhaseWaiting = "WaitingForCapacity"

	statusTrue  = "True"
	statusFalse = "False"
)

// Condition is one status condition, reduced to what kmctl shows.
type Condition struct {
	Status  string
	Reason  string
	Message string
}

// State is a Crew's status reduced to what the waits and the output need.
type State struct {
	Generation         int64
	ObservedGeneration int64
	Phase              string
	Ready              bool
	Message            string
	Capacity           *Condition // nil on a Kubemoot without CapacityAvailable
}

// Read extracts the State from a Crew's map form. Absent fields read as their zero
// value; nothing in a Crew's status is required.
func Read(obj map[string]any) State {
	s := State{}
	s.Generation, _, _ = unstructured.NestedInt64(obj, "metadata", "generation")
	s.ObservedGeneration, _, _ = unstructured.NestedInt64(obj, "status", "observedGeneration")
	s.Phase, _, _ = unstructured.NestedString(obj, "status", "phase")
	s.Ready, _, _ = unstructured.NestedBool(obj, "status", "ready")
	s.Message, _, _ = unstructured.NestedString(obj, "status", "message")
	s.Capacity = FindCondition(obj, ConditionCapacity)
	return s
}

// FindCondition returns the named condition from status.conditions, or nil.
func FindCondition(obj map[string]any, condType string) *Condition {
	conds, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != condType {
			continue
		}
		cond := &Condition{}
		cond.Status, _ = m["status"].(string)
		cond.Reason, _ = m["reason"].(string)
		cond.Message, _ = m["message"].(string)
		return cond
	}
	return nil
}

// stale reports a status that describes an older generation of the Crew. A status
// with no observedGeneration (0.95) is never stale.
func (s State) stale() bool {
	return s.ObservedGeneration > 0 && s.ObservedGeneration < s.Generation
}

// Reconciled reports whether the crew is reconciled as declared, with what it is
// still waiting for when it is not. It reads status.ready and the generation, never
// capacity: this is the question a rollout asks.
func (s State) Reconciled() (bool, string) {
	switch {
	case s.stale():
		return false, "waiting for the operator to reconcile the latest change"
	case s.Phase == "Error":
		return false, withMessage("the crew failed to reconcile", s.Message)
	case !s.Ready:
		return false, withMessage("waiting for the crew to reconcile", s.Message)
	}
	return true, ""
}

// CanAnswer reports whether the crew can answer a question now, with what it is
// waiting for when it cannot: the crew must be reconciled, in phase Ready, with
// CapacityAvailable True. On a Kubemoot without CapacityAvailable it is the
// reconciled crew whose phase is Ready (or not reported).
func (s State) CanAnswer() (bool, string) {
	if ok, why := s.Reconciled(); !ok {
		return false, why
	}
	if s.Capacity != nil && s.Capacity.Status != statusTrue {
		return false, s.capacityWaiting()
	}
	if s.Phase == PhaseWaiting {
		return false, withMessage("waiting for an accelerator", s.Message)
	}
	if s.Phase != "" && s.Phase != PhaseReady {
		return false, "waiting for the crew to become Ready (phase " + s.Phase + ")"
	}
	return true, ""
}

// capacityWaiting says what a crew without capacity is waiting for. False carries the
// scheduler's reason; Unknown means there is no running coordinator to assess yet.
func (s State) capacityWaiting() string {
	if s.Capacity.Status == statusFalse {
		return withMessage("waiting for an accelerator", s.Capacity.Message)
	}
	return withMessage("waiting for the coordinator to run", s.Capacity.Message)
}

func withMessage(prefix, message string) string {
	if message == "" {
		return prefix
	}
	return prefix + ": " + message
}
