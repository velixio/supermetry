package demo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/alerts"
	"supermetry/internal/platform/authz"
	"supermetry/internal/platform/store"
	"supermetry/internal/sites"
	"supermetry/internal/synth"
)

func (s *Service) createExamples(ctx context.Context, site sites.Site, ownerID int64) error {
	if err := pgx.BeginFunc(ctx, s.deps.Pool, func(tx pgx.Tx) error {
		for _, table := range []string{"alerts", "segments", "annotations", "funnels", "goals"} {
			if _, err := tx.Exec(ctx, "delete from "+table+" where site_id=$1", site.ID); err != nil {
				return fmt.Errorf("clear demo %s: %w", table, err)
			}
		}
		goals := []store.GoalRow{
			{Kind: "event", Match: "exact", Value: "Signup", Name: "Signup"},
			{Kind: "event", Match: "exact", Value: "Purchase", Name: "Purchase", Currency: "USD"},
			{Kind: "page", Match: "exact", Value: "/pricing", Name: "Visit pricing"},
			{Kind: "scroll", Match: "wildcard", Value: "/blog/*", Name: "Blog scroll 75%", ScrollThreshold: int16Pointer(75)},
		}
		for _, goal := range goals {
			if _, err := store.InsertGoal(ctx, tx, site.ID, goal); err != nil {
				return fmt.Errorf("insert demo goal %q: %w", goal.Name, err)
			}
		}
		steps := json.RawMessage(`[{"type":"page","match":"exact","value":"/"},{"type":"page","match":"exact","value":"/pricing"},{"type":"event","match":"exact","value":"Signup"},{"type":"event","match":"exact","value":"Purchase"}]`)
		if _, err := store.InsertFunnel(ctx, tx, site.ID, "Signup flow", steps); err != nil {
			return fmt.Errorf("insert demo funnel: %w", err)
		}
		if ownerID != 0 {
			campaignDay := s.deps.Now().In(site.TZ).AddDate(0, 0, -synth.DemoCampaignDaysAgo)
			dipDay := s.deps.Now().In(site.TZ).AddDate(0, 0, -synth.DemoDipDaysAgo)
			if _, err := store.InsertAnnotation(ctx, tx, site.ID, campaignDay, "Autumn launch campaign", ownerID); err != nil {
				return fmt.Errorf("insert demo campaign annotation: %w", err)
			}
			if _, err := store.InsertAnnotation(ctx, tx, site.ID, dipDay, "CDN outage", ownerID); err != nil {
				return fmt.Errorf("insert demo outage annotation: %w", err)
			}
			segments := []struct {
				name    string
				filters json.RawMessage
			}{
				{name: "Mobile visitors", filters: json.RawMessage(`[{"dimension":"device","operator":"is","values":["mobile"]}]`)},
				{name: "Paid search", filters: json.RawMessage(`[{"dimension":"channel","operator":"is","values":["Paid Search"]}]`)},
			}
			for _, segment := range segments {
				if _, err := store.InsertSegment(ctx, tx, site.ID, segment.name, segment.filters, ownerID); err != nil {
					return fmt.Errorf("insert demo segment %q: %w", segment.name, err)
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if s.deps.Definitions != nil {
		if err := s.deps.Definitions.Invalidate(); err != nil {
			return fmt.Errorf("invalidate demo definitions: %w", err)
		}
	}
	disabled := false
	_, err := s.deps.Alerts.CreateAlert(ctx, authz.Principal{
		UserID: ownerID, OrgID: site.OrgID, Role: authz.RoleOwner,
	}, site.ID, alerts.AlertInput{
		Kind: alerts.AlertTrafficSpike, Threshold: 2, ChannelIDs: []int64{}, Enabled: &disabled,
	})
	if err != nil {
		return fmt.Errorf("insert demo alert: %w", err)
	}
	return nil
}

func int16Pointer(value int16) *int16 { return &value }
