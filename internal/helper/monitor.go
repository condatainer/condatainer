package helper

// PollResult is the outcome of one non-blocking pass over a helper's events file.
type PollResult struct {
	NewMessages []MessageLine
	NewOffset   int64
	Start       *StartState // non-nil when a "start" event appears in this window
	Ready       *ReadyState // non-nil when a "ready" event appears in this window
	Done        *DoneState  // non-nil when a "done" event appears in this window
}

// PollOnce reads new events from the helper's events file starting at offset.
// Returns new messages, a start state, a ready state and a done state (each if its event
// fell within this window), and the advanced byte offset for the next call.
func PollOnce(id string, offset int64) PollResult {
	events, newOffset, _ := ReadEventsFrom(id, offset)
	result := PollResult{NewOffset: newOffset}
	for _, ev := range events {
		switch ev.Type {
		case "msg":
			result.NewMessages = append(result.NewMessages, MessageLine{
				Level: ev.Level, Text: ev.Text, Ts: ev.Ts,
			})
		case "start":
			result.Start = &StartState{Node: ev.Node, JobID: ev.JobID, Timestamp: ev.Ts}
		case "ready":
			result.Ready = &ReadyState{
				Port:        ev.Port,
				Node:        ev.Node,
				Timestamp:   ev.Ts,
				WalltimeSec: ev.WalltimeSec,
				JobID:       ev.JobID,
				URLPath:     ev.URLPath,
				ExternalURL: ev.ExternalURL,
			}
		case "done":
			exitCode := 0
			if ev.ExitCode != nil {
				exitCode = *ev.ExitCode
			}
			result.Done = &DoneState{ExitCode: exitCode, Ts: ev.Ts}
		}
	}
	return result
}
