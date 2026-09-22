import type { FanScenario } from './scenario';

export type SimulationStatus = 'queued' | 'running' | 'converged' | 'not_converged' | 'invalid_input' | 'failed';
export type RiskLevel = 'info' | 'warning' | 'critical';
export type DispositionAction = 'accept' | 'recalculate' | 'link_simulation';

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
  rule_code: string;
  entity_type: string;
  entity_id: number;
  action: DispositionAction;
  rationale: string;
  authorization_ref: string;
  linked_run_id?: number;
  disposed_by: number;
  disposed_by_email: string;
  disposed_at: string;
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
  risk_confirmed_at?: string;
  confirmation_note: string;
  dispositions?: RiskDisposition[] | null;
  scenario?: FanScenario;
}
