package storageusage

import "time"

const MeasurementVersion = 1

type Category string

const (
	CategoryHot             Category = "hot"
	CategoryRetained        Category = "retained"
	CategoryAuxiliary       Category = "auxiliary"
	CategoryReplayRecording Category = "replay_recording"
	CategoryReplayCSS       Category = "replay_css"
)

func (c Category) valid() bool {
	switch c {
	case CategoryHot, CategoryRetained, CategoryAuxiliary, CategoryReplayRecording, CategoryReplayCSS:
		return true
	default:
		return false
	}
}

type RunStatus string

const (
	RunActive    RunStatus = "active"
	RunComplete  RunStatus = "complete"
	RunAbandoned RunStatus = "abandoned"
)

type Run struct {
	ID                 int64
	Status             RunStatus
	MeasurementVersion int
	Cursor             string
	StartedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        *time.Time
	LastError          string
	SiteIDs            []int32
}

type Contribution struct {
	SiteID int32
	Bytes  int64
}

type Measurement struct {
	Category Category
	Values   []Contribution
}

type Status struct {
	LastCompletedAt *time.Time
	ActiveStartedAt *time.Time
	Pending         int
}
