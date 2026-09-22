package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"mine-ventilation-network-simulator/backend/internal/constants"
	"mine-ventilation-network-simulator/backend/internal/dto"
	"mine-ventilation-network-simulator/backend/internal/model"
	"mine-ventilation-network-simulator/backend/internal/repository"
	"mine-ventilation-network-simulator/backend/pkg/api"
)

const algorithmVersion = "air-balance-v1"

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
	pending, err := undisposedCriticalItems(run)
	if err != nil {
		return nil, api.Internal(err)
	}
	if len(pending) > 0 {
		return nil, api.Conflict("RISK_DISPOSITION_INCOMPLETE", fmt.Sprintf("还有 %d 条严重联锁风险未逐条处置，全部处置完成后才能确认整次风险", len(pending)))
	}
	updated, err := s.runs.ConfirmRisks(ctx, id, actor.ID, note, actor.Audit("simulation_run.risks_confirmed", "simulation_run"))
	if err != nil {
		return nil, mapRepositoryError(err, "推演风险确认")
	}
	return updated, nil
}

// DisposeRisk 逐条处置严重联锁风险。处置人必须不同于推演发起人；每条风险
// 证据只允许成功处置一次，数据库唯一索引保证重复或并发处置不会覆盖原证据。
func (s *SimulationService) DisposeRisk(ctx context.Context, id uint, input dto.DisposeRiskRequest, actor Actor) (*model.RiskDisposition, error) {
	run, err := s.runs.Find(ctx, id)
	if err != nil {
		return nil, mapRepositoryError(err, "推演记录")
	}
	if run.RunStatus == string(constants.SimulationStatusRunning) || run.RunStatus == string(constants.SimulationStatusQueued) {
		return nil, api.Conflict("SIMULATION_NOT_FINISHED", "推演完成后才能处置严重风险")
	}
	if actor.ID == run.StartedBy {
		return nil, api.Forbidden("复核员须不同于推演发起人，不能处置自己发起的推演风险")
	}
	if !constants.ValidDispositionAction(input.Action) {
		return nil, api.BadRequest("INVALID_DISPOSITION_ACTION", "处置方式必须是 accept、recalculate 或 link_simulation", nil)
	}
	risks, err := decodeRiskFlags(run.RiskFlagsJSON)
	if err != nil {
		return nil, api.Internal(err)
	}
	var target *dto.RiskEvidence
	for i := range risks {
		if risks[i].RuleCode == input.RuleCode && risks[i].EntityType == input.EntityType && risks[i].EntityID == input.EntityID {
			target = &risks[i]
			break
		}
	}
	if target == nil {
		return nil, api.BadRequest("RISK_ITEM_NOT_FOUND", "未找到匹配的风险证据条目，请刷新后重试", nil)
	}
	if target.Level != string(constants.RiskLevelCritical) {
		return nil, api.BadRequest("DISPOSITION_ONLY_FOR_CRITICAL", "只有严重联锁风险需要逐条处置，普通风险维持原有确认流程", nil)
	}
	disposition := &model.RiskDisposition{
		SimulationRunID: run.ID,
		RuleCode:        target.RuleCode,
		EntityType:      target.EntityType,
		EntityID:        target.EntityID,
		Action:          input.Action,
		Rationale:       strings.TrimSpace(input.Rationale),
		DisposedBy:      actor.ID,
		DisposedByEmail: actor.Email,
		DisposedAt:      time.Now().UTC(),
	}
	switch constants.DispositionAction(input.Action) {
	case constants.DispositionActionAccept:
		authorizationRef := strings.TrimSpace(input.AuthorizationRef)
		if len(authorizationRef) < 4 {
			return nil, api.BadRequest("AUTHORIZATION_REF_REQUIRED", "接受严重风险必须填写人工授权依据", nil)
		}
		disposition.AuthorizationRef = authorizationRef
	case constants.DispositionActionLinkSimulation:
		if input.LinkedRunID == 0 {
			return nil, api.BadRequest("LINKED_RUN_REQUIRED", "关联处置必须选择后续已完成推演", nil)
		}
		if err := s.validateLinkedRun(ctx, run, target, input.LinkedRunID); err != nil {
			return nil, err
		}
		linkedRunID := input.LinkedRunID
		disposition.LinkedRunID = &linkedRunID
	}
	audit := actor.Audit("simulation_run.risk_disposed", "risk_disposition")
	audit.Metadata = string(mustJSON(map[string]interface{}{
		"simulation_run_id": run.ID, "rule_code": target.RuleCode,
		"entity_type": target.EntityType, "entity_id": target.EntityID, "action": input.Action,
	}))
	if err := s.runs.CreateDisposition(ctx, disposition, audit); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, api.Conflict("DISPOSITION_ALREADY_EXISTS", "该风险条目已完成处置，重复或并发处置不会覆盖原证据")
		}
		return nil, mapRepositoryError(err, "风险处置")
	}
	return disposition, nil
}

// validateLinkedRun 校验关联推演可以作为处置依据：必须是当前推演之后已完成
// 的运行，其方案版本与网络快照与当前状态一致，且同一规则不再触发。
func (s *SimulationService) validateLinkedRun(ctx context.Context, run *model.SimulationRun, target *dto.RiskEvidence, linkedRunID uint) error {
	linked, err := s.runs.Find(ctx, linkedRunID)
	if err != nil {
		return mapRepositoryError(err, "关联推演")
	}
	if linked.RunStatus == string(constants.SimulationStatusRunning) || linked.RunStatus == string(constants.SimulationStatusQueued) {
		return api.Conflict("LINKED_RUN_NOT_FINISHED", "关联推演必须是已完成的运行")
	}
	subsequent := linked.StartedAt.After(run.StartedAt) || (linked.StartedAt.Equal(run.StartedAt) && linked.ID > run.ID)
	if !subsequent {
		return api.Conflict("LINKED_RUN_NOT_SUBSEQUENT", "关联推演必须是当前推演之后的运行")
	}
	scenario, err := s.scenarios.Find(ctx, run.ScenarioID)
	if err != nil {
		return mapRepositoryError(err, "风机方案")
	}
	var linkedSnapshot simulationSnapshot
	if err := json.Unmarshal(linked.InputSnapshotJSON, &linkedSnapshot); err != nil {
		return api.Internal(fmt.Errorf("decode linked run snapshot: %w", err))
	}
	if linkedSnapshot.Scenario.ID != scenario.ID || linkedSnapshot.Scenario.Version != scenario.Version {
		return api.Conflict("LINKED_RUN_SNAPSHOT_MISMATCH", "关联推演的方案版本必须与当前方案版本一致")
	}
	nodes, err := s.nodes.AllActive(ctx)
	if err != nil {
		return mapRepositoryError(err, "通风节点")
	}
	edges, err := s.edges.AllEnabled(ctx)
	if err != nil {
		return mapRepositoryError(err, "巷道边")
	}
	if !reflect.DeepEqual(normalizeViaJSON(nodes), normalizeViaJSON(linkedSnapshot.Nodes)) ||
		!reflect.DeepEqual(normalizeViaJSON(edges), normalizeViaJSON(linkedSnapshot.Edges)) {
		return api.Conflict("LINKED_RUN_SNAPSHOT_MISMATCH", "关联推演的网络快照必须与当前网络一致")
	}
	linkedRisks, err := decodeRiskFlags(linked.RiskFlagsJSON)
	if err != nil {
		return api.Internal(err)
	}
	for _, risk := range linkedRisks {
		if risk.RuleCode == target.RuleCode && risk.EntityType == target.EntityType && risk.EntityID == target.EntityID {
			return api.Conflict("LINKED_RULE_STILL_TRIGGERS", "关联推演中同一规则仍然触发，不能作为处置依据")
		}
	}
	return nil
}

func decodeRiskFlags(raw datatypes.JSON) ([]dto.RiskEvidence, error) {
	risks := []dto.RiskEvidence{}
	if err := json.Unmarshal(raw, &risks); err != nil {
		return nil, fmt.Errorf("decode risk flags: %w", err)
	}
	return risks, nil
}

func undisposedCriticalItems(run *model.SimulationRun) ([]dto.RiskEvidence, error) {
	risks, err := decodeRiskFlags(run.RiskFlagsJSON)
	if err != nil {
		return nil, err
	}
	disposed := make(map[string]bool, len(run.Dispositions))
	for _, item := range run.Dispositions {
		disposed[dispositionKey(item.RuleCode, item.EntityType, item.EntityID)] = true
	}
	pending := make([]dto.RiskEvidence, 0)
	for _, risk := range risks {
		if risk.Level != string(constants.RiskLevelCritical) {
			continue
		}
		if !disposed[dispositionKey(risk.RuleCode, risk.EntityType, risk.EntityID)] {
			pending = append(pending, risk)
		}
	}
	return pending, nil
}

func dispositionKey(ruleCode, entityType string, entityID uint) string {
	return fmt.Sprintf("%s|%s|%d", ruleCode, entityType, entityID)
}

// normalizeViaJSON 通过 JSON 往返归一化时间等字段表示，使快照与实时查询
// 结果可以按值比较。
func normalizeViaJSON[T any](value T) T {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var normalized T
	if err := json.Unmarshal(data, &normalized); err != nil {
		return value
	}
	return normalized
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
