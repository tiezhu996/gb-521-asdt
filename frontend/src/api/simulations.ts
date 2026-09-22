import { request, requestPage } from './client';
import type { DispositionAction, RiskDisposition, SimulationRun } from '../types/simulation';

export interface DisposeRiskPayload {
  rule_code: string;
  entity_type: string;
  entity_id: number;
  action: DispositionAction;
  rationale: string;
  authorization_ref?: string;
  linked_run_id?: number;
}

export const listSimulations = () => requestPage<SimulationRun>('/api/v1/simulations?page_size=100');
export const startSimulation = (scenario_id: number) => request<SimulationRun>('/api/v1/simulations', { method: 'POST', body: JSON.stringify({ scenario_id }) });
export const confirmRisks = (id: number, note: string) => request<SimulationRun>(`/api/v1/simulations/${id}/confirm-risks`, { method: 'POST', body: JSON.stringify({ note }) });
export const disposeRisk = (id: number, payload: DisposeRiskPayload) => request<RiskDisposition>(`/api/v1/simulations/${id}/dispositions`, { method: 'POST', body: JSON.stringify(payload) });
export const getSimulation = (id: number) => request<SimulationRun>(`/api/v1/simulations/${id}`);
