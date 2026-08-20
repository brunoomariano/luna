package store

import (
	"encoding/json"
	"fmt"

	"github.com/brunoomariano/luna/src/internal/fsm"
)

// The action names as they appear in the log. They are written down rather than
// derived from the Go type name because the log outlives the code: renaming a
// type should not make yesterday's tasks unreadable.
const (
	actionTaskCreated   = "TaskCreated"
	actionStatement     = "StatementRevised"
	actionGateChecks    = "GateChecksDeclared"
	actionAdvance       = "Advance"
	actionComplete      = "Complete"
	actionFail          = "Fail"
	actionGateApprove   = "GateApprove"
	actionGateAdjust    = "GateAdjust"
	actionGateReject    = "GateReject"
	actionGateJudged    = "GateJudged"
	actionReviewFinding = "ReviewFinding"
	actionBlock         = "Block"
	actionUnblock       = "Unblock"
	actionAbandon       = "Abandon"
	actionSetKnob       = "SetKnob"
)

// valueless are the actions that carry nothing but their name, so decoding them
// needs no JSON at all. Advance is deliberately absent: half of it is recorded and
// half is not, so it needs a case of its own.
var valueless = map[string]fsm.Action{
	actionGateApprove: fsm.GateApprove{},
	actionUnblock:     fsm.Unblock{},
}

// encodeAction turns an action into the pair of strings the log stores.
func encodeAction(action fsm.Action) (name, payload string, err error) {
	switch a := action.(type) {
	case fsm.GateApprove:
		return actionGateApprove, "", nil
	case fsm.Unblock:
		return actionUnblock, "", nil
	default:
		return encodeWithPayload(a)
	}
}

// encodeWithPayload handles the actions whose fields have to survive the log.
func encodeWithPayload(action fsm.Action) (name, payload string, err error) {
	switch a := action.(type) {
	case fsm.Advance:
		// Flow is tagged json:"-" and stays out; the gate decision goes in. That
		// split is the whole point — the flow is configuration and may change, the
		// decision is history and may not.
		return withPayload(actionAdvance, a)
	case fsm.TaskCreated:
		return withPayload(actionTaskCreated, a)
	case fsm.Complete:
		return withPayload(actionComplete, a)
	case fsm.Fail:
		return withPayload(actionFail, a)
	case fsm.GateAdjust:
		return withPayload(actionGateAdjust, a)
	case fsm.GateReject:
		return withPayload(actionGateReject, a)
	case fsm.GateJudged:
		return withPayload(actionGateJudged, a)
	case fsm.ReviewFinding:
		return withPayload(actionReviewFinding, a)
	case fsm.Block:
		return withPayload(actionBlock, a)
	default:
		return encodeDeclaration(action)
	}
}

// encodeDeclaration encodes what a person said *about* the task, as opposed to
// what happened to it.
//
// Split from the switch above for the complexity gate, along a seam the domain
// already has: a statement and a set of gate checks are both a person describing
// their own task, and neither moves it.
func encodeDeclaration(action fsm.Action) (name, payload string, err error) {
	switch a := action.(type) {
	case fsm.StatementRevised:
		return withPayload(actionStatement, a)
	case fsm.GateChecksDeclared:
		return withPayload(actionGateChecks, a)
	default:
		return encodeRunControl(action)
	}
}

// encodeRunControl encodes the actions a person takes about the run itself,
// rather than about a stage.
//
// Split from the switch above for the complexity gate, along the same seam the
// reducer's own dispatch splits on: everything there moves work through the flow,
// everything here changes the terms it runs under.
func encodeRunControl(action fsm.Action) (name, payload string, err error) {
	switch a := action.(type) {
	case fsm.Abandon:
		return withPayload(actionAbandon, a)
	case fsm.SetKnob:
		return withPayload(actionSetKnob, a)
	default:
		return "", "", fmt.Errorf("%w: cannot record %T", ErrUnknownAction, action)
	}
}

// decodeAction turns a stored event back into the action that produced it.
//
// An action this build does not recognise stops the replay rather than being
// skipped: a log written by a newer version would otherwise rebuild the task into
// a state it was never in, and that state would look perfectly valid.
func decodeAction(e Event, flow []fsm.Stage) (fsm.Action, error) {
	if action, ok := valueless[e.Action]; ok {
		return action, nil
	}

	// The two that need the flow injected back, which is what keeps them out of
	// the table below: half of each is recorded and half is supplied.
	switch e.Action {
	case actionAdvance:
		return decodeWithFlow(e.Payload, flow, func(a fsm.Advance) fsm.Advance {
			a.Flow = flow
			return a
		})
	case actionComplete:
		return decodeWithFlow(e.Payload, flow, func(a fsm.Complete) fsm.Complete {
			a.Flow = flow
			return a
		})
	case actionReviewFinding:
		return decodeWithFlow(e.Payload, flow, func(a fsm.ReviewFinding) fsm.ReviewFinding {
			a.Flow = flow
			return a
		})
	}

	if decode, ok := fromPayload[e.Action]; ok {
		return decode(e.Payload)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownAction, e.Action)
}

// fromPayload are the actions that rebuild from their JSON alone.
//
// A table rather than a switch arm each: they all say the same thing, and the
// list is long enough that the shape was hiding among the two cases that are
// genuinely different.
var fromPayload = map[string]func(string) (fsm.Action, error){
	actionTaskCreated: decodeJSON[fsm.TaskCreated],
	actionStatement:   decodeJSON[fsm.StatementRevised],
	actionGateChecks:  decodeJSON[fsm.GateChecksDeclared],
	actionFail:        decodeJSON[fsm.Fail],
	actionGateAdjust:  decodeJSON[fsm.GateAdjust],
	actionGateReject:  decodeJSON[fsm.GateReject],
	actionGateJudged:  decodeJSON[fsm.GateJudged],
	actionAbandon:     decodeJSON[fsm.Abandon],
	actionSetKnob:     decodeJSON[fsm.SetKnob],
	actionBlock:       decodeJSON[fsm.Block],
}

// decodeWithFlow rebuilds an action and supplies the flow it should check
// against.
//
// The flow comes from the caller rather than the log: storing it would freeze a
// task to the flow it started under, and flows are meant to be editable.
// Both actions that carry one are rebuilt this way, so the rule lives
// in one place rather than being repeated per action.
func decodeWithFlow[T fsm.Action](payload string, flow []fsm.Stage, withFlow func(T) T) (fsm.Action, error) {
	action, err := decodeJSON[T](payload)
	if err != nil {
		return nil, err
	}

	typed, ok := action.(T)
	if !ok {
		return nil, fmt.Errorf("%w: payload did not decode to a %T", ErrUnknownAction, typed)
	}
	return withFlow(typed), nil
}

// withPayload pairs an action name with its JSON body, so each case above stays
// one line instead of four.
func withPayload(name string, v any) (string, string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", "", fmt.Errorf("encoding %T: %w", v, err)
	}
	return name, string(b), nil
}

func decodeJSON[T fsm.Action](payload string) (fsm.Action, error) {
	var v T
	if payload == "" {
		return v, nil
	}
	if err := json.Unmarshal([]byte(payload), &v); err != nil {
		return nil, fmt.Errorf("decoding %T from %q: %w", v, payload, err)
	}
	return v, nil
}
