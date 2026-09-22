package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"mine-ventilation-network-simulator/backend/internal/constants"
	"mine-ventilation-network-simulator/backend/internal/dto"
	"mine-ventilation-network-simulator/backend/internal/model"
	"mine-ventilation-network-simulator/backend/internal/repository"
)

type dispositionFixture struct {
	db          *gorm.DB
	service     *SimulationService
	engineer    Actor
	reviewer    Actor
	otherReview Actor
	run         *model.SimulationRun
	followup    *model.SimulationRun
}

func newDispositionFixture(t *testing.T) dispositionFixture {
	t.Helper()
	dsn := filepath.ToSlash(filepath.Join(t.TempDir(), "disposition.db")) + "?_journal_mode=WAL&_txlock=immediate&_busy_timeout=10000&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.VentilationNode{}, &model.AirwayEdge{},
		&model.FanScenario{}, &model.SimulationRun{}, &model.RiskDisposition{}, &model.AuditEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	users := []model.User{
		{ID: 1, Email: "engineer@mine.local", DisplayName: "通风工程师", Role: string(constants.RoleEngineer), Active: true},
		{ID: 2, Email: "reviewer@mine.local", DisplayName: "安全复核员", Role: string(constants.RoleReviewer), Active: true},
		{ID: 3, Email: "reviewer2@mine.local", DisplayName: "二号复核员", Role: string(constants.RoleReviewer), Active: true},
	}
	for _, user := range users {
		if err := db.Create(&user).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	approvedBy := users[1].ID
	scenario := model.FanScenario{
		ID: 1, Name: "测试方案", Description: "fixture",
		FanCurveJSON:  datatypes.JSON([]byte(`[{"flow_m3s":0,"pressure_pa":1000},{"flow_m3s":30,"pressure_pa":500}]`)),
		OperatingMode: "normal", ScenarioStatus: string(constants.ScenarioStatusApproved),
		SolverTolerance: 0.02, MaxIterations: 100, Version: 2, CreatedBy: 1, ApprovedBy: &approvedBy,
	}
	if err := db.Create(&scenario).Error; err != nil {
		t.Fatalf("create scenario: %v", err)
	}
	nodes := []model.VentilationNode{
		{ID: 1, Code: "IN", NodeType: string(constants.NodeTypeIntake), PressurePa: 1000, Status: string(constants.NodeStatusActive)},
		{ID: 2, Code: "WF", NodeType: string(constants.NodeTypeWorkface), RequiredAirflowM3S: 18, PressurePa: 400, Status: string(constants.NodeStatusActive)},
		{ID: 3, Code: "OUT", NodeType: string(constants.NodeTypeExhaust), PressurePa: 0, Status: string(constants.NodeStatusActive)},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatalf("create node: %v", err)
		}
	}
	edges := []model.AirwayEdge{
		{ID: 1, Code: "E1", FromNodeID: 1, ToNodeID: 2, ResistanceNS2M8: 2, AreaM2: 8, MaxVelocityMS: 8, DoorState: string(constants.DoorStateOpen), Enabled: true, CriticalPath: true, Version: 1},
		{ID: 2, Code: "E2", FromNodeID: 2, ToNodeID: 3, ResistanceNS2M8: 2, AreaM2: 7, MaxVelocityMS: 8, DoorState: string(constants.DoorStateOpen), Enabled: true, CriticalPath: true, Version: 1},
	}
	for i := range edges {
		if err := db.Create(&edges[i]).Error; err != nil {
			t.Fatalf("create edge: %v", err)
		}
	}
	svc := NewSimulationService(
		repository.NewSimulationRunRepository(db),
		repository.NewFanScenarioRepository(db),
		repository.NewVentilationNodeRepository(db),
		repository.NewAirwayEdgeRepository(db),
	)
	f := dispositionFixture{
		db: db, service: svc,
		engineer:    Actor{ID: 1, Email: "engineer@mine.local", Name: "通风工程师", Role: string(constants.RoleEngineer)},
		reviewer:    Actor{ID: 2, Email: "reviewer@mine.local", Name: "安全复核员", Role: string(constants.RoleReviewer)},
		otherReview: Actor{ID: 3, Email: "reviewer2@mine.local", Name: "二号复核员", Role: string(constants.RoleReviewer)},
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return f
}

func riskFlags(t *testing.T, risks ...dto.RiskEvidence) datatypes.JSON {
	t.Helper()
	data, err := json.Marshal(risks)
	if err != nil {
		t.Fatalf("marshal risks: %v", err)
	}
	return datatypes.JSON(data)
}

func snapshotJSON(t *testing.T) datatypes.JSON {
	t.Helper()
	snapshot := simulationSnapshot{
		Scenario: model.FanScenario{ID: 1, Version: 2},
		Nodes: []model.VentilationNode{
			{ID: 1, Code: "IN", NodeType: "intake", PressurePa: 1000, Status: "active"},
			{ID: 2, Code: "WF", NodeType: "workface", RequiredAirflowM3S: 18, PressurePa: 400, Status: "active"},
			{ID: 3, Code: "OUT", NodeType: "exhaust", PressurePa: 0, Status: "active"},
		},
		Edges: []model.AirwayEdge{
			{ID: 1, Code: "E1", FromNodeID: 1, ToNodeID: 2, ResistanceNS2M8: 2, AreaM2: 8, MaxVelocityMS: 8, DoorState: "open", Enabled: true, CriticalPath: true, Version: 1},
			{ID: 2, Code: "E2", FromNodeID: 2, ToNodeID: 3, ResistanceNS2M8: 2, AreaM2: 7, MaxVelocityMS: 8, DoorState: "open", Enabled: true, CriticalPath: true, Version: 1},
		},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return datatypes.JSON(data)
}

func createRun(t *testing.T, f dispositionFixture, id uint, startedAt time.Time, risks datatypes.JSON, snapshot datatypes.JSON, startedBy uint) *model.SimulationRun {
	t.Helper()
	empty := datatypes.JSON([]byte(`{}`))
	run := &model.SimulationRun{
		ID: id, ScenarioID: 1, RunStatus: string(constants.SimulationStatusConverged),
		IterationCount: 3, Residual: 0.01,
		InputSnapshotJSON: snapshot, NodePressuresJSON: empty, EdgeFlowsJSON: empty,
		ResidualsJSON: datatypes.JSON([]byte(`[0.1,0.05,0.01]`)), RiskFlagsJSON: risks,
		AlgorithmVersion: algorithmVersion, StartedBy: startedBy, StartedAt: startedAt, FinishedAt: &startedAt,
	}
	if err := f.db.Create(run).Error; err != nil {
		t.Fatalf("create run %d: %v", id, err)
	}
	return run
}

func criticalVelocityRisk() dto.RiskEvidence {
	return dto.RiskEvidence{RuleCode: constants.RiskRuleVelocity, Level: string(constants.RiskLevelCritical), EntityType: "airway_edge", EntityID: 1, Evidence: 9.2, Threshold: 8, Unit: "m/s", Description: "计算风速超过巷道配置上限"}
}

func warningReverseRisk() dto.RiskEvidence {
	return dto.RiskEvidence{RuleCode: constants.RiskRuleReverseFlow, Level: string(constants.RiskLevelWarning), EntityType: "airway_edge", EntityID: 2, Evidence: -1.2, Threshold: 0, Unit: "m3/s", Description: "计算方向与巷道定义方向相反"}
}

func TestDisposeCriticalRiskAcceptRequiresManualAuthority(t *testing.T) {
	f := newDispositionFixture(t)
	f.run = createRun(t, f, 1, time.Now().Add(-2*time.Hour).UTC(), riskFlags(t, criticalVelocityRisk()), snapshotJSON(t), f.engineer.ID)
	_, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
		RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
		Decision: string(constants.RiskDispositionAcceptResidual), Rationale: "已核对现场风速记录",
	}, f.reviewer)
	if err == nil || !messageContains(err, "人工授权依据") {
		t.Fatalf("expected manual authority error, got %v", err)
	}
}

func TestDisposeCriticalRiskRejectsRunInitiator(t *testing.T) {
	f := newDispositionFixture(t)
	f.run = createRun(t, f, 1, time.Now().Add(-2*time.Hour).UTC(), riskFlags(t, criticalVelocityRisk()), snapshotJSON(t), f.engineer.ID)
	_, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
		RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
		Decision: string(constants.RiskDispositionReturnRecalc), Rationale: "退回调整风机曲线后重算",
	}, f.engineer)
	if err == nil || !messageContains(err, "不同于推演发起人") {
		t.Fatalf("expected initiator rejection, got %v", err)
	}
}

func TestDisposeCriticalRiskIgnoresNonCriticalAndUnknownKey(t *testing.T) {
	f := newDispositionFixture(t)
	f.run = createRun(t, f, 1, time.Now().Add(-2*time.Hour).UTC(), riskFlags(t, warningReverseRisk()), snapshotJSON(t), f.engineer.ID)
	_, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
		RiskKey:  MakeRiskKey(constants.RiskRuleReverseFlow, "airway_edge", 2),
		Decision: string(constants.RiskDispositionReturnRecalc), Rationale: "普通风险不应进入逐条处置",
	}, f.reviewer)
	if err == nil || !messageContains(err, "普通风险") {
		t.Fatalf("expected non-critical rejection, got %v", err)
	}
	_, err = f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
		RiskKey:  "AIR-VELOCITY-001|airway_edge|99",
		Decision: string(constants.RiskDispositionReturnRecalc), Rationale: "不存在的证据",
	}, f.reviewer)
	if err == nil || !messageContains(err, "不存在") {
		t.Fatalf("expected not found rejection, got %v", err)
	}
}

func TestDisposeCriticalRiskReturnRecalcSucceedsAndPersists(t *testing.T) {
	f := newDispositionFixture(t)
	f.run = createRun(t, f, 1, time.Now().Add(-2*time.Hour).UTC(), riskFlags(t, criticalVelocityRisk()), snapshotJSON(t), f.engineer.ID)
	updated, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
		RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
		Decision: string(constants.RiskDispositionReturnRecalc), Rationale: "风门状态待核对，退回重算",
	}, f.reviewer)
	if err != nil {
		t.Fatalf("dispose: %v", err)
	}
	if len(updated.RiskDispositions) != 1 {
		t.Fatalf("expected 1 disposition, got %d", len(updated.RiskDispositions))
	}
	got := updated.RiskDispositions[0]
	if got.DisposedBy != f.reviewer.ID || got.Decision != string(constants.RiskDispositionReturnRecalc) {
		t.Fatalf("unexpected disposition: %#v", got)
	}
	reloaded, err := f.service.Get(context.Background(), f.run.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.RiskDispositions) != 1 || reloaded.RiskDispositions[0].Rationale != "风门状态待核对，退回重算" {
		t.Fatalf("disposition not readable after reload: %#v", reloaded.RiskDispositions)
	}
}

func TestDisposeCriticalRiskIsIdempotentUnderConcurrency(t *testing.T) {
	f := newDispositionFixture(t)
	f.run = createRun(t, f, 1, time.Now().Add(-2*time.Hour).UTC(), riskFlags(t, criticalVelocityRisk()), snapshotJSON(t), f.engineer.ID)
	key := MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1)
	input := dto.DisposeRiskRequest{RiskKey: key, Decision: string(constants.RiskDispositionReturnRecalc), Rationale: "并发处置只成功一次"}
	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		actor := f.reviewer
		if i == 1 {
			actor = f.otherReview
		}
		go func(index int, act Actor) {
			defer wg.Done()
			<-start
			_, results[index] = f.service.DisposeCriticalRisk(context.Background(), f.run.ID, input, act)
		}(i, actor)
	}
	close(start)
	wg.Wait()
	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case messageContains(err, "已处置") || messageContains(err, "已存在"):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected exactly one success and one conflict, got success=%d conflict=%d", successes, conflicts)
	}
	var count int64
	if err := f.db.Model(&model.RiskDisposition{}).Where("simulation_run_id = ? AND risk_key = ?", f.run.ID, key).Count(&count).Error; err != nil {
		t.Fatalf("count dispositions: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one persisted disposition, got %d", count)
	}
	var audits int64
	if err := f.db.Model(&model.AuditEvent{}).Where("action = ?", "simulation_run.risk_disposed").Count(&audits).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("expected exactly one disposition audit, got %d", audits)
	}
}

func TestConfirmRisksBlockedUntilAllCriticalDisposed(t *testing.T) {
	f := newDispositionFixture(t)
	secondCritical := dto.RiskEvidence{RuleCode: constants.RiskRuleDemandGap, Level: string(constants.RiskLevelCritical), EntityType: "ventilation_node", EntityID: 2, Evidence: 12, Threshold: 18, Unit: "m3/s", Description: "工作面计算风量低于最低需风量"}
	f.run = createRun(t, f, 1, time.Now().Add(-2*time.Hour).UTC(), riskFlags(t, criticalVelocityRisk(), secondCritical, warningReverseRisk()), snapshotJSON(t), f.engineer.ID)
	if _, err := f.service.ConfirmRisks(context.Background(), f.run.ID, "两条严重风险均已核对", f.reviewer); err == nil || !messageContains(err, "未逐条处置") {
		t.Fatalf("expected pending critical block, got %v", err)
	}
	if _, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
		RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
		Decision: string(constants.RiskDispositionAcceptResidual), Rationale: "短期超限在规程允许窗口内",
		ManualAuthority: "矿总工 2026-09-21 书面授权编号 AQ-2026-091",
	}, f.reviewer); err != nil {
		t.Fatalf("dispose velocity: %v", err)
	}
	if _, err := f.service.ConfirmRisks(context.Background(), f.run.ID, "还剩一条严重风险", f.reviewer); err == nil || !messageContains(err, "1 条") {
		t.Fatalf("expected one pending critical block, got %v", err)
	}
	if _, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
		RiskKey:  MakeRiskKey(constants.RiskRuleDemandGap, "ventilation_node", 2),
		Decision: string(constants.RiskDispositionReturnRecalc), Rationale: "调整风门开度后重新推演",
	}, f.reviewer); err != nil {
		t.Fatalf("dispose demand gap: %v", err)
	}
	confirmed, err := f.service.ConfirmRisks(context.Background(), f.run.ID, "严重风险已逐条处置，普通反向流已现场核对", f.reviewer)
	if err != nil {
		t.Fatalf("confirm after all dispositions: %v", err)
	}
	if confirmed.RiskConfirmedByName != f.reviewer.Name {
		t.Fatalf("expected confirmer name %q, got %q", f.reviewer.Name, confirmed.RiskConfirmedByName)
	}
}

func TestLinkFollowupRequiresSameVersionSnapshotAndRuleCleared(t *testing.T) {
	f := newDispositionFixture(t)
	base := time.Now().Add(-2 * time.Hour).UTC()
	snapshot := snapshotJSON(t)
	f.run = createRun(t, f, 1, base, riskFlags(t, criticalVelocityRisk()), snapshot, f.engineer.ID)

	t.Run("rejects run started earlier than current run", func(t *testing.T) {
		earlier := createRun(t, f, 14, base.Add(-time.Hour), riskFlags(t), snapshot, f.engineer.ID)
		_, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
			RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
			Decision: string(constants.RiskDispositionLinkFollowup), Rationale: "更早的推演不能作为后续",
			LinkedRunID: &earlier.ID,
		}, f.reviewer)
		if err == nil || !messageContains(err, "后续推演") {
			t.Fatalf("expected not-followup error, got %v", err)
		}
	})
	t.Run("rejects run where rule still triggers", func(t *testing.T) {
		still := createRun(t, f, 10, base.Add(time.Hour), riskFlags(t, criticalVelocityRisk()), snapshot, f.engineer.ID)
		_, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
			RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
			Decision: string(constants.RiskDispositionLinkFollowup), Rationale: "后续推演证明规则不再触发",
			LinkedRunID: &still.ID,
		}, f.reviewer)
		if err == nil || !messageContains(err, "仍然触发") {
			t.Fatalf("expected rule-still-triggered error, got %v", err)
		}
	})
	t.Run("rejects changed scenario version", func(t *testing.T) {
		changed := snapshotJSON(t)
		var snap simulationSnapshot
		if err := json.Unmarshal(changed, &snap); err != nil {
			t.Fatal(err)
		}
		snap.Scenario.Version = 3
		data, _ := json.Marshal(snap)
		run := createRun(t, f, 11, base.Add(2*time.Hour), riskFlags(t), datatypes.JSON(data), f.engineer.ID)
		_, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
			RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
			Decision: string(constants.RiskDispositionLinkFollowup), Rationale: "版本不一致也应拒绝",
			LinkedRunID: &run.ID,
		}, f.reviewer)
		if err == nil || !messageContains(err, "方案版本") {
			t.Fatalf("expected version mismatch error, got %v", err)
		}
	})
	t.Run("rejects changed network snapshot", func(t *testing.T) {
		var snap simulationSnapshot
		if err := json.Unmarshal(snapshot, &snap); err != nil {
			t.Fatal(err)
		}
		snap.Edges[0].AreaM2 = 5.5
		data, _ := json.Marshal(snap)
		run := createRun(t, f, 12, base.Add(3*time.Hour), riskFlags(t, warningReverseRisk()), datatypes.JSON(data), f.engineer.ID)
		_, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
			RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
			Decision: string(constants.RiskDispositionLinkFollowup), Rationale: "网络变化应拒绝",
			LinkedRunID: &run.ID,
		}, f.reviewer)
		if err == nil || !messageContains(err, "网络快照") {
			t.Fatalf("expected snapshot mismatch error, got %v", err)
		}
	})
	t.Run("accepts later finished run with identical snapshot and rule cleared", func(t *testing.T) {
		ok := createRun(t, f, 13, base.Add(4*time.Hour), riskFlags(t, warningReverseRisk()), snapshot, f.engineer.ID)
		updated, err := f.service.DisposeCriticalRisk(context.Background(), f.run.ID, dto.DisposeRiskRequest{
			RiskKey:  MakeRiskKey(constants.RiskRuleVelocity, "airway_edge", 1),
			Decision: string(constants.RiskDispositionLinkFollowup), Rationale: "同版本同快照后续推演未再触发风速规则",
			LinkedRunID: &ok.ID,
		}, f.reviewer)
		if err != nil {
			t.Fatalf("link followup: %v", err)
		}
		d := updated.RiskDispositions[0]
		if d.LinkedRunID == nil || *d.LinkedRunID != ok.ID {
			t.Fatalf("expected linked run %d, got %#v", ok.ID, d.LinkedRunID)
		}
	})
}

func messageContains(err error, fragment string) bool {
	return err != nil && strings.Contains(err.Error(), fragment)
}
