import { request, requestPage } from './client';
import type { RiskDispositionDecision, SimulationRun } from '../types/simulation';

export const listSimulations = () => requestPage<SimulationRun>('/api/v1/simulations?page_size=100');
export const startSimulation = (scenario_id: number) => request<SimulationRun>('/api/v1/simulations', { method: 'POST', body: JSON.stringify({ scenario_id }) });
export const confirmRisks = (id: number, note: string) => request<SimulationRun>(`/api/v1/simulations/${id}/confirm-risks`, { method: 'POST', body: JSON.stringify({ note }) });
export const getSimulation = (id: number) => request<SimulationRun>(`/api/v1/simulations/${id}`);

export interface DisposeRiskInput {
  risk_key: string;
  decision: RiskDispositionDecision;
  rationale: string;
  manual_authority?: string;
  linked_run_id?: number;
}

export const disposeCriticalRisk = (runId: number, input: DisposeRiskInput) =>
  request<SimulationRun>(`/api/v1/simulations/${runId}/dispose-critical-risk`, {
    method: 'POST',
    body: JSON.stringify(input),
  });
