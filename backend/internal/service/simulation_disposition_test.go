package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"mine-ventilation-network-simulator/backend/internal/constants"
	"mine-ventilation-network-simulator/backend/internal/dto"
	"mine-ventilation-network-simulator/backend/internal/model"
	"mine-ventilation-network-simulator/backend/internal/repository"
	"mine-ventilation-network-simulator/backend/pkg/api"
)

var (
	dispositionStarter  = Actor{ID: 1, Email: "engineer@mine.local", Role: string(constants.RoleEngineer)}
	dispositionReviewer = Actor{ID: 2, Email: "reviewer@mine.local", Role: string(constants.RoleReviewer)}
)

func newDispositionFixture(t *testing.T) (*SimulationService, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:disposition-%s?mode=memory&cache=shared", strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.VentilationNode{}, &model.AirwayEdge{},
		&model.FanScenario{}, &model.SimulationRun{}, &model.RiskDisposition{}, &model.AuditEvent{},
	); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return NewSimulationService(
		repository.NewSimulationRunRepository(db),
		repository.NewFanScenarioRepository(db),
		repository.NewVentilationNodeRepository(db),
		repository.NewAirwayEdgeRepository(db),
	), db
}

func seedRiskyNetwork(t *testing.T, db *gorm.DB, maxVelocity float64) model.FanScenario {
	t.Helper()
	nodes := []model.VentilationNode{
		{Code: "IN", NodeType: string(constants.NodeTypeIntake), Status: string(constants.NodeStatusActive), PressurePa: 1000},
		{Code: "WF", NodeType: string(constants.NodeTypeWorkface), Status: string(constants.NodeStatusActive), RequiredAirflowM3S: 8, PressurePa: 500},
		{Code: "OUT", NodeType: string(constants.NodeTypeExhaust), Status: string(constants.NodeStatusActive), PressurePa: 0},
	}
	if err := db.Create(&nodes).Error; err != nil {
		t.Fatalf("seed nodes: %v", err)
	}
	edges := []model.AirwayEdge{
		{Code: "E1", FromNodeID: nodes[0].ID, ToNodeID: nodes[1].ID, ResistanceNS2M8: 2, AreaM2: 5, MaxVelocityMS: maxVelocity, DoorState: string(constants.DoorStateOpen), Enabled: true, CriticalPath: true, Version: 1},
		{Code: "E2", FromNodeID: nodes[1].ID, ToNodeID: nodes[2].ID, ResistanceNS2M8: 2, AreaM2: 5, MaxVelocityMS: maxVelocity, DoorState: string(constants.DoorStateOpen), Enabled: true, CriticalPath: true, Version: 1},
	}
	if err := db.Create(&edges).Error; err != nil {
		t.Fatalf("seed edges: %v", err)
	}
	curve := datatypes.JSON([]byte(`[{"flow_m3s":0,"pressure_pa":900},{"flow_m3s":30,"pressure_pa":600},{"flow_m3s":60,"pressure_pa":300}]`))
	scenario := model.FanScenario{
		Name: "处置测试方案", Description: "fixture", FanCurveJSON: curve, OperatingMode: "normal",
		ScenarioStatus: string(constants.ScenarioStatusApproved), SolverTolerance: 0.02,
		MaxIterations: 120, Version: 1, CreatedBy: dispositionStarter.ID,
	}
	if err := db.Create(&scenario).Error; err != nil {
		t.Fatalf("seed scenario: %v", err)
	}
	return scenario
}

func criticalRisks(t *testing.T, run *model.SimulationRun) []dto.RiskEvidence {
	t.Helper()
	risks, err := decodeRiskFlags(run.RiskFlagsJSON)
	if err != nil {
		t.Fatalf("decode risks: %v", err)
	}
	critical := make([]dto.RiskEvidence, 0)
	for _, risk := range risks {
		if risk.Level == string(constants.RiskLevelCritical) {
			critical = append(critical, risk)
		}
	}
	if len(critical) == 0 {
		t.Fatalf("expected critical risks in run %d, got %#v", run.ID, risks)
	}
	return critical
}

func appErrorCode(err error) string {
	var appErr *api.AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}
	return ""
}

func disposeRequest(risk dto.RiskEvidence, action constants.DispositionAction) dto.DisposeRiskRequest {
	return dto.DisposeRiskRequest{
		RuleCode: risk.RuleCode, EntityType: risk.EntityType, EntityID: risk.EntityID,
		Action: string(action), Rationale: "复核后按规程处置该严重风险",
	}
}

func TestDisposeRiskRejectsSimulationInitiator(t *testing.T) {
	svc, db := newDispositionFixture(t)
	scenario := seedRiskyNetwork(t, db, 2)
	run, err := svc.Start(context.Background(), scenario.ID, dispositionStarter)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	_, err = svc.DisposeRisk(context.Background(), run.ID, disposeRequest(criticalRisks(t, run)[0], constants.DispositionActionRecalculate), dispositionStarter)
	if appErrorCode(err) != "ACCESS_DENIED" {
		t.Fatalf("initiator must not dispose own run risks, got %v", err)
	}
}

func TestDisposeRiskAcceptRequiresAuthorizationRef(t *testing.T) {
	svc, db := newDispositionFixture(t)
	scenario := seedRiskyNetwork(t, db, 2)
	run, err := svc.Start(context.Background(), scenario.ID, dispositionStarter)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	input := disposeRequest(criticalRisks(t, run)[0], constants.DispositionActionAccept)
	if _, err := svc.DisposeRisk(context.Background(), run.ID, input, dispositionReviewer); appErrorCode(err) != "AUTHORIZATION_REF_REQUIRED" {
		t.Fatalf("accept without authorization ref must fail, got %v", err)
	}
	input.AuthorizationRef = "矿总工程师批准单 2026-091"
	disposition, err := svc.DisposeRisk(context.Background(), run.ID, input, dispositionReviewer)
	if err != nil {
		t.Fatalf("accept with authorization ref: %v", err)
	}
	if disposition.AuthorizationRef != input.AuthorizationRef || disposition.DisposedBy != dispositionReviewer.ID || disposition.DisposedByEmail != dispositionReviewer.Email {
		t.Fatalf("disposition evidence incomplete: %#v", disposition)
	}
	reloaded, err := svc.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if len(reloaded.Dispositions) != 1 || reloaded.Dispositions[0].Rationale != input.Rationale {
		t.Fatalf("disposition not persisted with run: %#v", reloaded.Dispositions)
	}
}

func TestDisposeRiskDuplicateDoesNotOverwrite(t *testing.T) {
	svc, db := newDispositionFixture(t)
	scenario := seedRiskyNetwork(t, db, 2)
	run, err := svc.Start(context.Background(), scenario.ID, dispositionStarter)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	input := disposeRequest(criticalRisks(t, run)[0], constants.DispositionActionRecalculate)
	if _, err := svc.DisposeRisk(context.Background(), run.ID, input, dispositionReviewer); err != nil {
		t.Fatalf("first disposition: %v", err)
	}
	overwrite := disposeRequest(criticalRisks(t, run)[0], constants.DispositionActionAccept)
	overwrite.Rationale = "试图覆盖原处置证据"
	overwrite.AuthorizationRef = "伪造授权依据"
	if _, err := svc.DisposeRisk(context.Background(), run.ID, overwrite, dispositionReviewer); appErrorCode(err) != "DISPOSITION_ALREADY_EXISTS" {
		t.Fatalf("duplicate disposition must conflict, got %v", err)
	}
	reloaded, err := svc.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	if len(reloaded.Dispositions) != 1 {
		t.Fatalf("expected exactly one disposition, got %d", len(reloaded.Dispositions))
	}
	kept := reloaded.Dispositions[0]
	if kept.Action != string(constants.DispositionActionRecalculate) || kept.Rationale != input.Rationale || kept.AuthorizationRef != "" {
		t.Fatalf("original evidence was overwritten: %#v", kept)
	}
}

func TestConfirmRisksBlockedUntilAllCriticalDisposed(t *testing.T) {
	svc, db := newDispositionFixture(t)
	scenario := seedRiskyNetwork(t, db, 2)
	run, err := svc.Start(context.Background(), scenario.ID, dispositionStarter)
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	critical := criticalRisks(t, run)
	if len(critical) < 2 {
		t.Fatalf("fixture should produce at least two critical risks, got %#v", critical)
	}
	if _, err := svc.ConfirmRisks(context.Background(), run.ID, "确认整次风险", dispositionReviewer); appErrorCode(err) != "RISK_DISPOSITION_INCOMPLETE" {
		t.Fatalf("confirm must be blocked while critical risks pending, got %v", err)
	}
	first := disposeRequest(critical[0], constants.DispositionActionRecalculate)
	if _, err := svc.DisposeRisk(context.Background(), run.ID, first, dispositionReviewer); err != nil {
		t.Fatalf("dispose first item: %v", err)
	}
	if _, err := svc.ConfirmRisks(context.Background(), run.ID, "确认整次风险", dispositionReviewer); appErrorCode(err) != "RISK_DISPOSITION_INCOMPLETE" {
		t.Fatalf("confirm must stay blocked until all critical risks disposed, got %v", err)
	}
	second := disposeRequest(critical[1], constants.DispositionActionAccept)
	second.AuthorizationRef = "矿总工程师批准单 2026-092"
	if _, err := svc.DisposeRisk(context.Background(), run.ID, second, dispositionReviewer); err != nil {
		t.Fatalf("dispose second item: %v", err)
	}
	confirmed, err := svc.ConfirmRisks(context.Background(), run.ID, "全部严重风险已逐条处置", dispositionReviewer)
	if err != nil {
		t.Fatalf("confirm after full disposition: %v", err)
	}
	if confirmed.RiskConfirmedBy == nil || *confirmed.RiskConfirmedBy != dispositionReviewer.ID {
		t.Fatalf("confirmation not recorded: %#v", confirmed.RiskConfirmedBy)
	}
}

func TestDisposeRiskRejectsNonCriticalAndUnknownItems(t *testing.T) {
	svc, db := newDispositionFixture(t)
	scenario := seedRiskyNetwork(t, db, 50)
	now := time.Now().UTC()
	run := model.SimulationRun{
		ScenarioID: scenario.ID, RunStatus: string(constants.SimulationStatusConverged),
		InputSnapshotJSON: datatypes.JSON([]byte(`{"scenario":{},"nodes":[],"edges":[]}`)),
		NodePressuresJSON: datatypes.JSON([]byte(`{}`)), EdgeFlowsJSON: datatypes.JSON([]byte(`{}`)),
		ResidualsJSON: datatypes.JSON([]byte(`[]`)),
		RiskFlagsJSON: mustJSON([]dto.RiskEvidence{
			{RuleCode: constants.RiskRuleReverseFlow, Level: string(constants.RiskLevelWarning), EntityType: "airway_edge", EntityID: 1, Evidence: -1.2, Threshold: 0, Unit: "m3/s", Description: "反向流"},
		}),
		AlgorithmVersion: algorithmVersion, StartedBy: dispositionStarter.ID, StartedAt: now, FinishedAt: &now,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("insert run: %v", err)
	}
	warning := disposeRequest(dto.RiskEvidence{RuleCode: constants.RiskRuleReverseFlow, EntityType: "airway_edge", EntityID: 1}, constants.DispositionActionRecalculate)
	if _, err := svc.DisposeRisk(context.Background(), run.ID, warning, dispositionReviewer); appErrorCode(err) != "DISPOSITION_ONLY_FOR_CRITICAL" {
		t.Fatalf("warning risk must not need disposition, got %v", err)
	}
	unknown := disposeRequest(dto.RiskEvidence{RuleCode: constants.RiskRuleVelocity, EntityType: "airway_edge", EntityID: 999}, constants.DispositionActionRecalculate)
	if _, err := svc.DisposeRisk(context.Background(), run.ID, unknown, dispositionReviewer); appErrorCode(err) != "RISK_ITEM_NOT_FOUND" {
		t.Fatalf("unknown risk item must fail, got %v", err)
	}
	if _, err := svc.ConfirmRisks(context.Background(), run.ID, "普通风险维持原确认流程", dispositionReviewer); err != nil {
		t.Fatalf("warning-only run must confirm without dispositions: %v", err)
	}
}

func TestDisposeRiskLinkSimulationValidatesSuccessor(t *testing.T) {
	svc, db := newDispositionFixture(t)
	scenario := seedRiskyNetwork(t, db, 2)
	ctx := context.Background()
	run1, err := svc.Start(ctx, scenario.ID, dispositionStarter)
	if err != nil {
		t.Fatalf("start run1: %v", err)
	}
	run2, err := svc.Start(ctx, scenario.ID, dispositionStarter)
	if err != nil {
		t.Fatalf("start run2: %v", err)
	}
	critical := criticalRisks(t, run1)
	link := func(linkedID uint, risk dto.RiskEvidence) error {
		input := disposeRequest(risk, constants.DispositionActionLinkSimulation)
		input.LinkedRunID = linkedID
		_, err := svc.DisposeRisk(ctx, run1.ID, input, dispositionReviewer)
		return err
	}
	if err := link(run2.ID, critical[0]); appErrorCode(err) != "LINKED_RULE_STILL_TRIGGERS" {
		t.Fatalf("link to run where rule still triggers must fail, got %v", err)
	}
	if err := link(run1.ID, critical[0]); appErrorCode(err) != "LINKED_RUN_NOT_SUBSEQUENT" {
		t.Fatalf("link to self must fail, got %v", err)
	}
	if err := db.Model(&model.AirwayEdge{}).Where("1 = 1").Update("max_velocity_ms", 50).Error; err != nil {
		t.Fatalf("fix network: %v", err)
	}
	run3, err := svc.Start(ctx, scenario.ID, dispositionStarter)
	if err != nil {
		t.Fatalf("start run3: %v", err)
	}
	if risks, _ := decodeRiskFlags(run3.RiskFlagsJSON); len(risks) != 0 {
		t.Fatalf("run3 should be clean after fix, got %#v", risks)
	}
	if err := link(run3.ID, critical[0]); err != nil {
		t.Fatalf("link to clean subsequent run: %v", err)
	}
	if err := db.Model(&model.AirwayEdge{}).Where("1 = 1").Update("max_velocity_ms", 60).Error; err != nil {
		t.Fatalf("change network again: %v", err)
	}
	if err := link(run3.ID, critical[1]); appErrorCode(err) != "LINKED_RUN_SNAPSHOT_MISMATCH" {
		t.Fatalf("stale linked snapshot must fail, got %v", err)
	}
	if err := link(99999, critical[1]); appErrorCode(err) != "NOT_FOUND" {
		t.Fatalf("missing linked run must fail, got %v", err)
	}
}
