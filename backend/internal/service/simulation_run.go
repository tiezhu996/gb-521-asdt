package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/datatypes"

	"mine-ventilation-network-simulator/backend/internal/constants"
	"mine-ventilation-network-simulator/backend/internal/dto"
	"mine-ventilation-network-simulator/backend/internal/model"
	"mine-ventilation-network-simulator/backend/internal/repository"
	"mine-ventilation-network-simulator/backend/pkg/api"
)

const algorithmVersion = "air-balance-v1"

var zeroTime = time.Unix(0, 0).UTC()

type SimulationService struct {
	runs      *repository.SimulationRunRepository
	scenarios *repository.FanScenarioRepository
	nodes     *repository.VentilationNodeRepository
	edges     *repository.AirwayEdgeRepository
}

type simulationSnapshot struct {
	Scenario model.FanScenario       `json:"scenario"`
	Nodes    []model.VentilationNode `json:"nodes"`
	Edges    []model.AirwayEdge      `json:"edges"`
}

type solverResult struct {
	Status        constants.SimulationStatus
	Iterations    int
	Residual      float64
	Pressures     map[uint]float64
	Flows         map[uint]float64
	Residuals     []float64
	Risks         []dto.RiskEvidence
	NetworkIssues []dto.NetworkIssue
}

func NewSimulationService(runs *repository.SimulationRunRepository, scenarios *repository.FanScenarioRepository, nodes *repository.VentilationNodeRepository, edges *repository.AirwayEdgeRepository) *SimulationService {
	return &SimulationService{runs: runs, scenarios: scenarios, nodes: nodes, edges: edges}
}

func (s *SimulationService) List(ctx context.Context, query dto.SimulationListQuery) ([]model.SimulationRun, int64, int, int, error) {
	page, pageSize := normalizePage(query.Page, query.PageSize)
	if query.Status != "" && !constants.ValidSimulationStatus(query.Status) {
		return nil, 0, page, pageSize, api.BadRequest("INVALID_SIMULATION_STATUS", "推演状态筛选值无效", nil)
	}
	items, total, err := s.runs.List(ctx, page, pageSize, query.Status, query.ScenarioID)
	if err != nil {
		return nil, 0, page, pageSize, mapRepositoryError(err, "推演记录")
	}
	return items, total, page, pageSize, nil
}

func (s *SimulationService) Get(ctx context.Context, id uint) (*model.SimulationRun, error) {
	item, err := s.runs.Find(ctx, id)
	return item, mapRepositoryError(err, "推演记录")
}

func (s *SimulationService) Start(ctx context.Context, scenarioID uint, actor Actor) (*model.SimulationRun, error) {
	scenario, err := s.scenarios.Find(ctx, scenarioID)
	if err != nil {
		return nil, mapRepositoryError(err, "风机方案")
	}
	if scenario.ScenarioStatus != string(constants.ScenarioStatusApproved) {
		return nil, api.Conflict("SCENARIO_NOT_APPROVED", "只有已批准方案可以发起离线推演")
	}
	nodes, err := s.nodes.AllActive(ctx)
	if err != nil {
		return nil, mapRepositoryError(err, "通风节点")
	}
	edges, err := s.edges.AllEnabled(ctx)
	if err != nil {
		return nil, mapRepositoryError(err, "巷道边")
	}
	result := solveNetwork(*scenario, nodes, edges)
	now := time.Now().UTC()
	snapshotJSON := mustJSON(simulationSnapshot{Scenario: *scenario, Nodes: nodes, Edges: edges})
	run := &model.SimulationRun{
		ScenarioID: scenario.ID, RunStatus: string(result.Status), IterationCount: result.Iterations,
		Residual: result.Residual, InputSnapshotJSON: snapshotJSON,
		NodePressuresJSON: mustJSON(result.Pressures), EdgeFlowsJSON: mustJSON(result.Flows),
		ResidualsJSON: mustJSON(result.Residuals), RiskFlagsJSON: mustJSON(result.Risks),
		AlgorithmVersion: algorithmVersion, StartedBy: actor.ID, StartedAt: now, FinishedAt: &now,
	}
	audit := actor.Audit("simulation_run.started", "simulation_run")
	audit.Metadata = string(mustJSON(map[string]interface{}{
		"scenario_id": scenario.ID, "result_status": result.Status,
		"network_issues": result.NetworkIssues,
	}))
	if err := s.runs.Create(ctx, run, audit); err != nil {
		return nil, mapRepositoryError(err, "推演记录")
	}
	return run, nil
}

func (s *SimulationService) ConfirmRisks(ctx context.Context, id uint, note string, actor Actor) (*model.SimulationRun, error) {
	run, err := s.runs.Find(ctx, id)
	if err != nil {
		return nil, mapRepositoryError(err, "推演记录")
	}
	if run.RunStatus == string(constants.SimulationStatusRunning) || run.RunStatus == string(constants.SimulationStatusQueued) {
		return nil, api.Conflict("SIMULATION_NOT_FINISHED", "推演完成后才能确认风险证据")
	}
	if actor.ID == run.StartedBy {
		return nil, api.Forbidden("复核员必须不同于推演发起人，不能由本人确认本次风险")
	}
	if pending := PendingCriticalRiskKeys(run); len(pending) > 0 {
		return nil, api.Conflict("CRITICAL_RISKS_PENDING", fmt.Sprintf("尚有 %d 条严重联锁风险未逐条处置，未全部处置不得确认整次风险", len(pending)))
	}
	updated, err := s.runs.ConfirmRisks(ctx, id, actor.ID, actor.Name, actor.Email, note, actor.Audit("simulation_run.risks_confirmed", "simulation_run"))
	if err != nil {
		return nil, mapRepositoryError(err, "推演风险确认")
	}
	return updated, nil
}

func solveNetwork(scenario model.FanScenario, nodes []model.VentilationNode, edges []model.AirwayEdge) solverResult {
	validation := validateDirectedNetwork(nodes, edges)
	result := solverResult{
		Status: constants.SimulationStatusInvalidInput, Pressures: map[uint]float64{},
		Flows: map[uint]float64{}, Residuals: []float64{}, Risks: []dto.RiskEvidence{}, NetworkIssues: validation.Issues,
	}
	for _, node := range nodes {
		result.Pressures[node.ID] = node.PressurePa
	}
	if !validation.Valid {
		result.Risks = disconnectedRisks(edges, validation.Issues)
		return result
	}
	curve := []dto.FanCurvePoint{}
	if err := json.Unmarshal(scenario.FanCurveJSON, &curve); err != nil || len(curve) < 2 {
		result.NetworkIssues = append(result.NetworkIssues, dto.NetworkIssue{Code: "INVALID_FAN_CURVE", EntityType: "fan_scenario", EntityID: scenario.ID, Message: "风机曲线无法解析", Severity: "critical"})
		return result
	}
	fixed := make(map[uint]bool)
	basePressure := make(map[uint]float64)
	for _, node := range nodes {
		basePressure[node.ID] = node.PressurePa
		if node.NodeType == string(constants.NodeTypeIntake) || node.NodeType == string(constants.NodeTypeExhaust) {
			fixed[node.ID] = true
		}
	}
	maxIterations := scenario.MaxIterations
	if maxIterations < 10 {
		maxIterations = 80
	}
	tolerance := scenario.SolverTolerance
	if tolerance <= 0 {
		tolerance = 0.02
	}
	for iteration := 1; iteration <= maxIterations; iteration++ {
		totalIntake := 0.0
		for _, edge := range edges {
			flow := edgeFlow(result.Pressures[edge.FromNodeID]-result.Pressures[edge.ToNodeID], edge)
			result.Flows[edge.ID] = flow
			if fixed[edge.FromNodeID] && flow > 0 {
				totalIntake += flow
			}
		}
		fanPressure := interpolateFanPressure(curve, totalIntake)
		for _, node := range nodes {
			if node.NodeType == string(constants.NodeTypeIntake) {
				result.Pressures[node.ID] = basePressure[node.ID] + fanPressure
			}
		}
		balance := make(map[uint]float64, len(nodes))
		derivative := make(map[uint]float64, len(nodes))
		for _, edge := range edges {
			flow := edgeFlow(result.Pressures[edge.FromNodeID]-result.Pressures[edge.ToNodeID], edge)
			result.Flows[edge.ID] = flow
			balance[edge.FromNodeID] -= flow
			balance[edge.ToNodeID] += flow
			dp := math.Abs(result.Pressures[edge.FromNodeID] - result.Pressures[edge.ToNodeID])
			r := effectiveResistance(edge)
			slope := 1 / (2 * math.Sqrt(r*math.Max(dp, 1)))
			derivative[edge.FromNodeID] += slope
			derivative[edge.ToNodeID] += slope
		}
		maxResidual := 0.0
		for _, node := range nodes {
			if fixed[node.ID] {
				continue
			}
			if abs := math.Abs(balance[node.ID]); abs > maxResidual {
				maxResidual = abs
			}
			if derivative[node.ID] > 0 {
				correction := 0.55 * balance[node.ID] / derivative[node.ID]
				correction = math.Max(-250, math.Min(250, correction))
				result.Pressures[node.ID] += correction
			}
		}
		result.Residuals = append(result.Residuals, round(maxResidual, 6))
		result.Iterations = iteration
		result.Residual = round(maxResidual, 6)
		if maxResidual <= tolerance {
			result.Status = constants.SimulationStatusConverged
			break
		}
	}
	if result.Status != constants.SimulationStatusConverged {
		result.Status = constants.SimulationStatusNotConverged
	}
	for id, value := range result.Pressures {
		result.Pressures[id] = round(value, 4)
	}
	for id, value := range result.Flows {
		result.Flows[id] = round(value, 4)
	}
	result.Risks = evaluateRisks(nodes, edges, result.Flows)
	return result
}

func edgeFlow(deltaPressure float64, edge model.AirwayEdge) float64 {
	if !edge.Enabled || edge.DoorState == string(constants.DoorStateClosed) {
		return 0
	}
	if deltaPressure == 0 {
		return 0
	}
	flow := math.Sqrt(math.Abs(deltaPressure) / effectiveResistance(edge))
	if deltaPressure < 0 {
		flow = -flow
	}
	return flow
}

func effectiveResistance(edge model.AirwayEdge) float64 {
	return math.Max(0.0001, edge.ResistanceNS2M8*constants.DoorResistanceMultiplier(edge.DoorState))
}

func interpolateFanPressure(points []dto.FanCurvePoint, flow float64) float64 {
	if flow <= points[0].FlowM3S {
		return points[0].PressurePa
	}
	for i := 1; i < len(points); i++ {
		if flow <= points[i].FlowM3S {
			ratio := (flow - points[i-1].FlowM3S) / (points[i].FlowM3S - points[i-1].FlowM3S)
			return points[i-1].PressurePa + ratio*(points[i].PressurePa-points[i-1].PressurePa)
		}
	}
	return math.Max(0, points[len(points)-1].PressurePa)
}

func evaluateRisks(nodes []model.VentilationNode, edges []model.AirwayEdge, flows map[uint]float64) []dto.RiskEvidence {
	risks := make([]dto.RiskEvidence, 0)
	incoming := make(map[uint]float64)
	outgoing := make(map[uint]float64)
	for _, edge := range edges {
		flow := flows[edge.ID]
		velocity := math.Abs(flow) / math.Max(edge.AreaM2, 0.001)
		if velocity > edge.MaxVelocityMS {
			risks = append(risks, dto.RiskEvidence{RuleCode: constants.RiskRuleVelocity, Level: string(constants.RiskLevelCritical), EntityType: "airway_edge", EntityID: edge.ID, Evidence: round(velocity, 3), Threshold: edge.MaxVelocityMS, Unit: "m/s", Description: "计算风速超过巷道配置上限"})
		}
		if flow < -0.01 {
			risks = append(risks, dto.RiskEvidence{RuleCode: constants.RiskRuleReverseFlow, Level: string(constants.RiskLevelWarning), EntityType: "airway_edge", EntityID: edge.ID, Evidence: round(flow, 3), Threshold: 0, Unit: "m3/s", Description: "计算方向与巷道定义方向相反"})
			incoming[edge.FromNodeID] += -flow
			outgoing[edge.ToNodeID] += -flow
		} else {
			outgoing[edge.FromNodeID] += flow
			incoming[edge.ToNodeID] += flow
		}
		if edge.CriticalPath && (!edge.Enabled || edge.DoorState == string(constants.DoorStateClosed) || math.Abs(flow) < 0.01) {
			risks = append(risks, dto.RiskEvidence{RuleCode: constants.RiskRuleDisconnected, Level: string(constants.RiskLevelCritical), EntityType: "airway_edge", EntityID: edge.ID, Evidence: round(math.Abs(flow), 3), Threshold: 0.01, Unit: "m3/s", Description: "关键路径无有效风量"})
		}
	}
	for _, node := range nodes {
		if node.NodeType != string(constants.NodeTypeWorkface) || node.RequiredAirflowM3S <= 0 {
			continue
		}
		available := math.Max(incoming[node.ID], outgoing[node.ID])
		if available < node.RequiredAirflowM3S {
			risks = append(risks, dto.RiskEvidence{RuleCode: constants.RiskRuleDemandGap, Level: string(constants.RiskLevelCritical), EntityType: "ventilation_node", EntityID: node.ID, Evidence: round(available, 3), Threshold: node.RequiredAirflowM3S, Unit: "m3/s", Description: "工作面计算风量低于最低需风量"})
		}
	}
	sort.Slice(risks, func(i, j int) bool {
		if risks[i].RuleCode == risks[j].RuleCode {
			return risks[i].EntityID < risks[j].EntityID
		}
		return risks[i].RuleCode < risks[j].RuleCode
	})
	return risks
}

func disconnectedRisks(edges []model.AirwayEdge, issues []dto.NetworkIssue) []dto.RiskEvidence {
	risks := make([]dto.RiskEvidence, 0)
	for _, issue := range issues {
		if issue.Severity == "critical" {
			risks = append(risks, dto.RiskEvidence{RuleCode: constants.RiskRuleDisconnected, Level: string(constants.RiskLevelCritical), EntityType: issue.EntityType, EntityID: issue.EntityID, Evidence: 0, Threshold: 1, Unit: "reachable", Description: issue.Message})
		}
	}
	for _, edge := range edges {
		if edge.CriticalPath && (!edge.Enabled || edge.DoorState == string(constants.DoorStateClosed)) {
			risks = append(risks, dto.RiskEvidence{RuleCode: constants.RiskRuleDisconnected, Level: string(constants.RiskLevelCritical), EntityType: "airway_edge", EntityID: edge.ID, Evidence: 0, Threshold: 1, Unit: "enabled", Description: "关键路径边被停用或关闭"})
		}
	}
	return risks
}

func mustJSON(value interface{}) datatypes.JSON {
	data, err := json.Marshal(value)
	if err != nil {
		return datatypes.JSON([]byte(`null`))
	}
	return datatypes.JSON(data)
}

func round(value float64, precision int) float64 {
	factor := math.Pow10(precision)
	return math.Round(value*factor) / factor
}

func simulationSummary(run model.SimulationRun) string {
	return fmt.Sprintf("simulation %d (%s), iterations=%d residual=%.6f", run.ID, run.RunStatus, run.IterationCount, run.Residual)
}

// MakeRiskKey builds the stable identifier of one rule evidence item inside a
// run. Risks are produced deterministically and stored read-only, so
// (rule_code, entity_type, entity_id) uniquely identifies one item.
func MakeRiskKey(ruleCode, entityType string, entityID uint) string {
	return fmt.Sprintf("%s|%s|%d", strings.TrimSpace(ruleCode), strings.TrimSpace(entityType), entityID)
}

// findRiskEvidence returns the immutable risk evidence item identified by key.
func findRiskEvidence(run *model.SimulationRun, riskKey string) (*dto.RiskEvidence, bool) {
	var risks []dto.RiskEvidence
	if err := json.Unmarshal(run.RiskFlagsJSON, &risks); err != nil {
		return nil, false
	}
	for i := range risks {
		if MakeRiskKey(risks[i].RuleCode, risks[i].EntityType, risks[i].EntityID) == riskKey {
			return &risks[i], true
		}
	}
	return nil, false
}

func runFinished(status string) bool {
	return status != string(constants.SimulationStatusQueued) && status != string(constants.SimulationStatusRunning)
}

func riskDispositionIndex(run *model.SimulationRun) map[string]model.RiskDisposition {
	index := make(map[string]model.RiskDisposition, len(run.RiskDispositions))
	for _, item := range run.RiskDispositions {
		index[item.RiskKey] = item
	}
	return index
}

// PendingCriticalRiskKeys lists critical evidence items without a disposition;
// the whole-run confirmation stays blocked while this is non-empty.
func PendingCriticalRiskKeys(run *model.SimulationRun) []string {
	var risks []dto.RiskEvidence
	if err := json.Unmarshal(run.RiskFlagsJSON, &risks); err != nil {
		return nil
	}
	disposed := riskDispositionIndex(run)
	pending := make([]string, 0)
	for _, risk := range risks {
		if risk.Level != string(constants.RiskLevelCritical) {
			continue
		}
		key := MakeRiskKey(risk.RuleCode, risk.EntityType, risk.EntityID)
		if _, ok := disposed[key]; !ok {
			pending = append(pending, key)
		}
	}
	return pending
}

// DisposeCriticalRisk records one reviewer decision for one critical risk item.
// The reviewer must not be the engineer who started the run, every decision
// needs a written rationale, accepting residual risk additionally requires a
// manual authorization basis, and linking a follow-up run is only allowed when
// that run is later, finished, used the same scenario version and an identical
// network snapshot, and the same rule no longer triggers in it.
func (s *SimulationService) DisposeCriticalRisk(ctx context.Context, runID uint, input dto.DisposeRiskRequest, actor Actor) (*model.SimulationRun, error) {
	riskKey := strings.TrimSpace(input.RiskKey)
	if !constants.ValidRiskDispositionDecision(input.Decision) {
		return nil, api.BadRequest("INVALID_DISPOSITION_DECISION", "处置决定必须是 accept_residual、return_recalc 或 link_followup", nil)
	}
	rationale := strings.TrimSpace(input.Rationale)
	if len([]rune(rationale)) < 4 {
		return nil, api.BadRequest("INVALID_DISPOSITION_RATIONALE", "必须写明逐条处置依据（至少 4 个字符）", nil)
	}
	if input.Decision == string(constants.RiskDispositionAcceptResidual) && strings.TrimSpace(input.ManualAuthority) == "" {
		return nil, api.BadRequest("MANUAL_AUTHORITY_REQUIRED", "接受严重剩余风险必须填写人工授权依据", nil)
	}
	if input.Decision == string(constants.RiskDispositionLinkFollowup) && (input.LinkedRunID == nil || *input.LinkedRunID == 0) {
		return nil, api.BadRequest("LINKED_RUN_REQUIRED", "关联处置必须选择后续已完成推演", nil)
	}

	run, err := s.runs.Find(ctx, runID)
	if err != nil {
		return nil, mapRepositoryError(err, "推演记录")
	}
	if !runFinished(run.RunStatus) {
		return nil, api.Conflict("SIMULATION_NOT_FINISHED", "推演完成后才能处置严重风险证据")
	}
	if run.RiskConfirmedAt != nil {
		return nil, api.Conflict("RISKS_ALREADY_CONFIRMED", "整次风险已确认，处置证据不可再追加或修改")
	}
	if actor.ID == run.StartedBy {
		return nil, api.Forbidden("复核员必须不同于推演发起人，不能由本人处置本次严重风险")
	}
	risk, ok := findRiskEvidence(run, riskKey)
	if !ok {
		return nil, api.NotFound("严重风险证据")
	}
	if risk.Level != string(constants.RiskLevelCritical) {
		return nil, api.BadRequest("NOT_CRITICAL_RISK", "逐条处置仅适用于严重（critical）联锁风险，普通风险沿用整次确认", nil)
	}
	if _, exists := riskDispositionIndex(run)[riskKey]; exists {
		return nil, api.Conflict("RISK_ALREADY_DISPOSED", "该条严重风险已处置，重复或并发提交不会覆盖原证据")
	}

	var linkedRunID *uint
	if input.Decision == string(constants.RiskDispositionLinkFollowup) {
		linked, err := s.validateFollowupLink(ctx, run, riskKey, *input.LinkedRunID)
		if err != nil {
			return nil, err
		}
		linkedRunID = &linked.ID
	}

	disposition := model.RiskDisposition{
		SimulationRunID: run.ID, RiskKey: riskKey,
		RuleCode: risk.RuleCode, Level: risk.Level, EntityType: risk.EntityType, EntityID: risk.EntityID,
		Decision: input.Decision, Rationale: rationale,
		ManualAuthority: strings.TrimSpace(input.ManualAuthority),
		LinkedRunID:     linkedRunID,
		DisposedBy:      actor.ID, DisposedByName: actor.Name, DisposedByEmail: actor.Email,
	}
	updated, err := s.runs.DisposeCriticalRisk(ctx, disposition, actor.Audit("simulation_run.risk_disposed", "simulation_run"))
	if err != nil {
		return nil, mapRepositoryError(err, "严重风险处置")
	}
	return updated, nil
}

func (s *SimulationService) validateFollowupLink(ctx context.Context, run *model.SimulationRun, riskKey string, linkedID uint) (*model.SimulationRun, error) {
	if linkedID == run.ID {
		return nil, api.BadRequest("LINKED_RUN_SAME_RUN", "不能将本次推演关联为自身的后续推演", nil)
	}
	linked, err := s.runs.Find(ctx, linkedID)
	if err != nil {
		return nil, mapRepositoryError(err, "后续推演记录")
	}
	if linked.ScenarioID != run.ScenarioID {
		return nil, api.Conflict("LINKED_RUN_SCENARIO_MISMATCH", "后续推演必须基于同一个风机方案")
	}
	if !runFinished(linked.RunStatus) {
		return nil, api.Conflict("LINKED_RUN_NOT_FINISHED", "被关联推演必须已完成计算")
	}
	// IDs are assigned monotonically; comparing both id and started_at keeps
	// the "later run" requirement intact even when timestamps coincide.
	if linked.ID <= run.ID || linked.StartedAt.Before(run.StartedAt) {
		return nil, api.Conflict("LINKED_RUN_NOT_FOLLOWUP", "只能关联在本次推演之后发起的后续推演")
	}
	currentSnapshot, linkedSnapshot, err := loadSnapshotPair(run, linked)
	if err != nil {
		return nil, err
	}
	if currentSnapshot.Scenario.Version != linkedSnapshot.Scenario.Version {
		return nil, api.Conflict("LINKED_RUN_VERSION_MISMATCH", fmt.Sprintf("后续推演的方案版本（v%d）与本次（v%d）不一致", linkedSnapshot.Scenario.Version, currentSnapshot.Scenario.Version))
	}
	if !sameNetworkSnapshot(currentSnapshot, linkedSnapshot) {
		return nil, api.Conflict("LINKED_RUN_SNAPSHOT_MISMATCH", "后续推演的网络快照（节点与巷道）与本次不一致")
	}
	if _, stillTriggered := findRiskEvidence(linked, riskKey); stillTriggered {
		return nil, api.Conflict("LINKED_RUN_RULE_STILL_TRIGGERED", "同一规则在后续推演中仍然触发，不能作为不再触发的证据")
	}
	return linked, nil
}

func loadSnapshotPair(current, linked *model.SimulationRun) (simulationSnapshot, simulationSnapshot, error) {
	var currentSnapshot, linkedSnapshot simulationSnapshot
	if err := json.Unmarshal(current.InputSnapshotJSON, &currentSnapshot); err != nil {
		return currentSnapshot, linkedSnapshot, api.Internal(fmt.Errorf("parse current simulation snapshot: %w", err))
	}
	if err := json.Unmarshal(linked.InputSnapshotJSON, &linkedSnapshot); err != nil {
		return currentSnapshot, linkedSnapshot, api.Internal(fmt.Errorf("parse linked simulation snapshot: %w", err))
	}
	return currentSnapshot, linkedSnapshot, nil
}

// sameNetworkSnapshot compares the network part of two input snapshots,
// excluding bookkeeping timestamps so that identical network + scenario
// versions compare equal across separate deterministic runs.
func sameNetworkSnapshot(a, b simulationSnapshot) bool {
	if len(a.Nodes) != len(b.Nodes) || len(a.Edges) != len(b.Edges) {
		return false
	}
	canonicalNode := func(node model.VentilationNode) model.VentilationNode {
		node.CreatedAt = zeroTime
		node.UpdatedAt = zeroTime
		return node
	}
	canonicalEdge := func(edge model.AirwayEdge) model.AirwayEdge {
		edge.CreatedAt = zeroTime
		edge.UpdatedAt = zeroTime
		edge.FromNode = model.VentilationNode{}
		edge.ToNode = model.VentilationNode{}
		return edge
	}
	nodeByID := make(map[uint]model.VentilationNode, len(b.Nodes))
	for _, node := range b.Nodes {
		nodeByID[node.ID] = canonicalNode(node)
	}
	for _, node := range a.Nodes {
		other, ok := nodeByID[node.ID]
		if !ok || canonicalNode(node) != other {
			return false
		}
	}
	edgeByID := make(map[uint]model.AirwayEdge, len(b.Edges))
	for _, edge := range b.Edges {
		edgeByID[edge.ID] = canonicalEdge(edge)
	}
	for _, edge := range a.Edges {
		other, ok := edgeByID[edge.ID]
		if !ok || canonicalEdge(edge) != other {
			return false
		}
	}
	return true
}
