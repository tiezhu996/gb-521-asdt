package model

import (
	"time"

	"gorm.io/datatypes"
)

type SimulationRun struct {
	ID                   uint              `gorm:"primaryKey" json:"id"`
	ScenarioID           uint              `gorm:"not null;index" json:"scenario_id"`
	RunStatus            string            `gorm:"size:24;not null;index;check:run_status IN ('queued','running','converged','not_converged','invalid_input','failed')" json:"run_status"`
	IterationCount       int               `gorm:"not null;default:0" json:"iteration_count"`
	Residual             float64           `gorm:"not null;default:0" json:"residual"`
	InputSnapshotJSON    datatypes.JSON    `gorm:"type:jsonb;not null" json:"input_snapshot_json"`
	NodePressuresJSON    datatypes.JSON    `gorm:"type:jsonb;not null" json:"node_pressures_json"`
	EdgeFlowsJSON        datatypes.JSON    `gorm:"type:jsonb;not null" json:"edge_flows_json"`
	ResidualsJSON        datatypes.JSON    `gorm:"type:jsonb;not null" json:"residuals_json"`
	RiskFlagsJSON        datatypes.JSON    `gorm:"type:jsonb;not null" json:"risk_flags_json"`
	AlgorithmVersion     string            `gorm:"size:32;not null" json:"algorithm_version"`
	StartedBy            uint              `gorm:"not null;index" json:"started_by"`
	StartedAt            time.Time         `json:"started_at"`
	FinishedAt           *time.Time        `json:"finished_at"`
	RiskConfirmedBy      *uint             `gorm:"index" json:"risk_confirmed_by"`
	RiskConfirmedByName  string            `gorm:"size:80" json:"risk_confirmed_by_name"`
	RiskConfirmedByEmail string            `gorm:"size:160" json:"risk_confirmed_by_email"`
	RiskConfirmedAt      *time.Time        `json:"risk_confirmed_at"`
	ConfirmationNote     string            `gorm:"size:500" json:"confirmation_note"`
	Scenario             FanScenario       `gorm:"foreignKey:ScenarioID" json:"scenario,omitempty"`
	RiskDispositions     []RiskDisposition `gorm:"foreignKey:SimulationRunID" json:"risk_dispositions"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

// RiskDisposition stores the reviewer's one-time, per-item decision for a
// single critical risk evidence row of one simulation run. The composite
// unique index on (simulation_run_id, risk_key) is the concurrency boundary
// that makes repeated or concurrent dispositions succeed only once.
type RiskDisposition struct {
	ID              uint          `gorm:"primaryKey" json:"id"`
	SimulationRunID uint          `gorm:"not null;index;uniqueIndex:idx_risk_disposition_key,priority:1" json:"simulation_run_id"`
	RiskKey         string        `gorm:"size:120;not null;uniqueIndex:idx_risk_disposition_key,priority:2" json:"risk_key"`
	RuleCode        string        `gorm:"size:40;not null" json:"rule_code"`
	Level           string        `gorm:"size:20;not null;check:level IN ('info','warning','critical')" json:"level"`
	EntityType      string        `gorm:"size:40;not null" json:"entity_type"`
	EntityID        uint          `gorm:"not null" json:"entity_id"`
	Decision        string        `gorm:"size:24;not null;index;check:decision IN ('accept_residual','return_recalc','link_followup')" json:"decision"`
	Rationale       string        `gorm:"size:500;not null" json:"rationale"`
	ManualAuthority string        `gorm:"size:500" json:"manual_authority"`
	LinkedRunID     *uint         `gorm:"index" json:"linked_run_id"`
	DisposedBy      uint          `gorm:"not null;index" json:"disposed_by"`
	DisposedByName  string        `gorm:"size:80;not null" json:"disposed_by_name"`
	DisposedByEmail string        `gorm:"size:160;not null" json:"disposed_by_email"`
	CreatedAt       time.Time     `json:"created_at"`
	SimulationRun   SimulationRun `gorm:"foreignKey:SimulationRunID" json:"-"`
}

func (SimulationRun) TableName() string   { return "simulation_runs" }
func (RiskDisposition) TableName() string { return "risk_dispositions" }
