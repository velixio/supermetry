package alerts

import "context"

// triggerReceiverLister is the optional repository capability that tells which
// channels got an incident's trigger event.
type triggerReceiverLister interface {
	TriggerReceivers(ctx context.Context, alertID, incidentID int64) (map[int64]bool, error)
}

// resolveReceivers narrows the channels of a resolve notification: a
// PagerDuty channel gets the resolve only when it received (and accepted) the
// incident's trigger, because a resolve for an unknown dedup key pages no one
// and a failed trigger must not be "resolved". Other channel kinds, and every
// non-resolve notification, are untouched.
func (e *Evaluator) resolveReceivers(ctx context.Context, alertID int64, n Notification, channels []DeliveryChannel) ([]DeliveryChannel, error) {
	if n.Rule == nil || n.Rule.Status != ruleStatusResolve || n.Rule.IncidentID == 0 {
		return channels, nil
	}
	hasPagerDuty := false
	for _, channel := range channels {
		hasPagerDuty = hasPagerDuty || channel.Kind == ChannelPagerDuty
	}
	lister, ok := e.repository.(triggerReceiverLister)
	if !hasPagerDuty || !ok {
		return channels, nil
	}
	received, err := lister.TriggerReceivers(ctx, alertID, n.Rule.IncidentID)
	if err != nil {
		return nil, err
	}
	kept := make([]DeliveryChannel, 0, len(channels))
	for _, channel := range channels {
		if channel.Kind == ChannelPagerDuty && !received[channel.ID] {
			continue
		}
		kept = append(kept, channel)
	}
	return kept, nil
}
