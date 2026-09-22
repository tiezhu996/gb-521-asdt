package constants

type SimulationStatus string

const (
	SimulationStatusQueued       SimulationStatus = "queued"
	SimulationStatusRunning      SimulationStatus = "running"
	SimulationStatusConverged    SimulationStatus = "converged"
	SimulationStatusNotConverged SimulationStatus = "not_converged"
	SimulationStatusInvalidInput SimulationStatus = "invalid_input"
	SimulationStatusFailed       SimulationStatus = "failed"
)

type RiskLevel string

const (
	RiskLevelInfo     RiskLevel = "info"
	RiskLevelWarning  RiskLevel = "warning"
	RiskLevelCritical RiskLevel = "critical"
)

type RiskDispositionDecision string

const (
	RiskDispositionAcceptResidual RiskDispositionDecision = "accept_residual"
	RiskDispositionReturnRecalc   RiskDispositionDecision = "return_recalc"
	RiskDispositionLinkFollowup   RiskDispositionDecision = "link_followup"
)

func ValidRiskDispositionDecision(value string) bool {
	switch RiskDispositionDecision(value) {
	case RiskDispositionAcceptResidual, RiskDispositionReturnRecalc, RiskDispositionLinkFollowup:
		return true
	default:
		return false
	}
}

func ValidSimulationStatus(value string) bool {
	switch SimulationStatus(value) {
	case SimulationStatusQueued, SimulationStatusRunning, SimulationStatusConverged,
		SimulationStatusNotConverged, SimulationStatusInvalidInput, SimulationStatusFailed:
		return true
	default:
		return false
	}
}

const (
	RiskRuleVelocity     = "AIR-VELOCITY-001"
	RiskRuleReverseFlow  = "AIR-DIRECTION-002"
	RiskRuleDemandGap    = "AIR-DEMAND-003"
	RiskRuleDisconnected = "AIR-PATH-004"
)
