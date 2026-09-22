package model

import "time"

// RiskDisposition 记录一条严重联锁风险的人工处置证据。每条风险证据
// （rule_code + entity_type + entity_id）在同一次推演中只允许处置一次，
// 唯一索引保证重复或并发处置只成功一次，且任何失败都不会覆盖既有证据。
type RiskDisposition struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	SimulationRunID  uint      `gorm:"not null;uniqueIndex:uk_risk_disposition_item" json:"simulation_run_id"`
	RuleCode         string    `gorm:"size:40;not null;uniqueIndex:uk_risk_disposition_item" json:"rule_code"`
	EntityType       string    `gorm:"size:40;not null;uniqueIndex:uk_risk_disposition_item" json:"entity_type"`
	EntityID         uint      `gorm:"not null;uniqueIndex:uk_risk_disposition_item" json:"entity_id"`
	Action           string    `gorm:"size:24;not null;check:action IN ('accept','recalculate','link_simulation')" json:"action"`
	Rationale        string    `gorm:"size:500;not null" json:"rationale"`
	AuthorizationRef string    `gorm:"size:200" json:"authorization_ref"`
	LinkedRunID      *uint     `gorm:"index" json:"linked_run_id"`
	DisposedBy       uint      `gorm:"not null;index" json:"disposed_by"`
	DisposedByEmail  string    `gorm:"size:160;not null" json:"disposed_by_email"`
	DisposedAt       time.Time `gorm:"not null" json:"disposed_at"`
	CreatedAt        time.Time `json:"created_at"`
}

func (RiskDisposition) TableName() string { return "risk_dispositions" }
