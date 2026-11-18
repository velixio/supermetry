package alerts

import (
	"context"
	"fmt"
	"strconv"
)

// TriggerReceivers returns the channels whose firing notification for the
// incident was delivered, so a resolve goes only where the trigger landed.
func (s *Service) TriggerReceivers(ctx context.Context, alertID, incidentID int64) (map[int64]bool, error) {
	rows, err := s.store.Pool.Query(ctx, `select distinct channel_id from alert_delivery_log
		where alert_id=$1 and status='succeeded'
		  and notification->'rule'->>'incident_id' = $2::text
		  and notification->'rule'->>'status' = 'firing'`, alertID, strconv.FormatInt(incidentID, 10))
	if err != nil {
		return nil, fmt.Errorf("read incident trigger deliveries: %w", err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
