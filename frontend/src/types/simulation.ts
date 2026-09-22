import type { FanScenario } from './scenario';

export type SimulationStatus = 'queued' | 'running' | 'converged' | 'not_converged' | 'invalid_input' | 'failed';
export type RiskLevel = 'info' | 'warning' | 'critical';
export type RiskDispositionDecision = 'accept_residual' | 'return_recalc' | 'link_followup';

export interface RiskEvidence {
  rule_code: string;
  level: RiskLevel;
  entity_type: string;
  entity_id: number;
  evidence: number;
  threshold: number;
  unit: string;
  description: string;
}

export interface RiskDisposition {
  id: number;
  simulation_run_id: number;
  risk_key: string;
  rule_code: string;
  level: RiskLevel;
  entity_type: string;
  entity_id: number;
  decision: RiskDispositionDecision;
  rationale: string;
  manual_authority: string;
  linked_run_id?: number;
  disposed_by: number;
  disposed_by_name: string;
  disposed_by_email: string;
  created_at: string;
}

export interface SimulationRun {
  id: number;
  scenario_id: number;
  run_status: SimulationStatus;
  iteration_count: number;
  residual: number;
  input_snapshot_json: unknown;
  node_pressures_json: Record<string, number>;
  edge_flows_json: Record<string, number>;
  residuals_json: number[];
  risk_flags_json: RiskEvidence[];
  algorithm_version: string;
  started_by: number;
  started_at: string;
  finished_at?: string;
  risk_confirmed_by?: number;
  risk_confirmed_by_name: string;
  risk_confirmed_by_email: string;
  risk_confirmed_at?: string;
  confirmation_note: string;
  risk_dispositions: RiskDisposition[];
  scenario?: FanScenario;
}

export function makeRiskKey(risk: Pick<RiskEvidence, 'rule_code' | 'entity_type' | 'entity_id'>): string {
  return `${risk.rule_code}|${risk.entity_type}|${risk.entity_id}`;
}

export const finishedSimulationStatuses: SimulationStatus[] = ['converged', 'not_converged', 'invalid_input', 'failed'];
