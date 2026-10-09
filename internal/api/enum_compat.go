package api

// Keep the established generated enum names available after adding the site
// currency state enum changed oapi-codegen's collision disambiguation.
const (
	Event    = FunnelStepTypeEvent
	Page     = FunnelStepTypePage
	Exact    = GoalInputMatchExact
	Prefix   = GoalInputMatchPrefix
	Wildcard = GoalInputMatchWildcard
)
