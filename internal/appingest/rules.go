package appingest

import "supermetry/internal/apprules"

// ruleFrames lists the frames of an event for the rule matchers.
func ruleFrames(body Body) []apprules.Frame {
	n := 0
	for _, ex := range body.Exceptions {
		n += len(ex.Frames)
	}
	if n == 0 {
		return nil
	}
	out := make([]apprules.Frame, 0, n)
	for _, ex := range body.Exceptions {
		for _, f := range ex.Frames {
			out = append(out, apprules.Frame{Filename: f.Filename, Module: f.Module, InApp: f.InApp})
		}
	}
	return out
}

func matchGroupingRule(ev Event, body Body, rules *apprules.Set) (string, bool) {
	if rules == nil || rules.Grouping.Len() == 0 {
		return "", false
	}
	in := apprules.GroupInput{Frames: ruleFrames(body), Tags: body.Tags}
	in.ErrorTypes = append(in.ErrorTypes, ev.ErrorType)
	for _, ex := range body.Exceptions {
		in.ErrorTypes = append(in.ErrorTypes, ex.Type)
		in.Messages = append(in.Messages, ex.Value)
	}
	if body.Message != "" {
		in.Messages = append(in.Messages, body.Message)
	}
	return rules.Grouping.Match(in)
}

// OwnerOf returns the user the project's ownership rules route the event's
// issue to, or 0. It is only a candidate: the writer assigns it when (and only
// when) the event creates the issue. rules may be nil.
func OwnerOf(body Body, rules *apprules.Set) int64 {
	if rules == nil || rules.Ownership.Len() == 0 {
		return 0
	}
	owner, _ := rules.Ownership.Owner(ruleFrames(body), body.Tags)
	return owner
}
