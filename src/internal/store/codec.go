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
	actionAdvance       = "Advance"
	actionComplete      = "Complete"
	actionFail          = "Fail"
	actionGateApprove   = "GateApprove"
	actionGateAdjust    = "GateAdjust"
	actionGateReject    = "GateReject"
	actionReviewFinding = "ReviewFinding"
	actionUnblock       = "Unblock"
)

// encodeAction turns an action into the pair of strings the log stores.
//
// Advance carries the flow, which is configuration rather than history: recording
// it would freeze a task to the flow it started under, and the flow is meant to be
// editable (ADR-0017). Replay supplies the current one instead.
func encodeAction(action fsm.Action) (name, payload string, err error) {
	switch a := action.(type) {
	case fsm.Advance:
		return actionAdvance, "", nil
	case fsm.GateApprove:
		return actionGateApprove, "", nil
	case fsm.Unblock:
		return actionUnblock, "", nil
	case fsm.Complete:
		return withPayload(actionComplete, a)
	case fsm.Fail:
		return withPayload(actionFail, a)
	case fsm.GateAdjust:
		return withPayload(actionGateAdjust, a)
	case fsm.GateReject:
		return withPayload(actionGateReject, a)
	case fsm.ReviewFinding:
		return withPayload(actionReviewFinding, a)
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
	switch e.Action {
	case actionAdvance:
		return fsm.Advance{Flow: flow}, nil
	case actionGateApprove:
		return fsm.GateApprove{}, nil
	case actionUnblock:
		return fsm.Unblock{}, nil
	case actionComplete:
		return decodeJSON[fsm.Complete](e.Payload)
	case actionFail:
		return decodeJSON[fsm.Fail](e.Payload)
	case actionGateAdjust:
		return decodeJSON[fsm.GateAdjust](e.Payload)
	case actionGateReject:
		return decodeJSON[fsm.GateReject](e.Payload)
	case actionReviewFinding:
		return decodeJSON[fsm.ReviewFinding](e.Payload)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownAction, e.Action)
	}
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
