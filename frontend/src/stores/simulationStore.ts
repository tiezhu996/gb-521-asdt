import { create } from 'zustand';
import * as simulationApi from '../api/simulations';
import type { DisposeRiskInput } from '../api/simulations';
import type { SimulationRun } from '../types/simulation';

interface SimulationState {
  runs: SimulationRun[];
  selected: SimulationRun | null;
  loading: boolean;
  load(): Promise<void>;
  select(id: number): Promise<void>;
  start(scenarioId: number): Promise<SimulationRun>;
  confirm(id: number, note: string): Promise<void>;
  dispose(id: number, input: DisposeRiskInput): Promise<SimulationRun>;
}

function mergeRun(runs: SimulationRun[], updated: SimulationRun): SimulationRun[] {
  return runs.map((run) => (run.id === updated.id ? updated : run));
}

export const useSimulationStore = create<SimulationState>((set, get) => ({
  runs: [], selected: null, loading: false,
  async load() {
    set({ loading: true });
    try {
      const { items } = await simulationApi.listSimulations();
      const selectedId = get().selected?.id;
      set({ runs: items, selected: selectedId ? items.find((run) => run.id === selectedId) ?? null : get().selected });
    } finally { set({ loading: false }); }
  },
  async select(id) { set({ selected: await simulationApi.getSimulation(id) }); },
  async start(scenarioId) {
    const run = await simulationApi.startSimulation(scenarioId);
    set({ selected: run, runs: (await simulationApi.listSimulations()).items });
    return run;
  },
  async confirm(id, note) {
    const updated = await simulationApi.confirmRisks(id, note);
    set({ selected: updated, runs: mergeRun(get().runs, updated) });
  },
  async dispose(id, input) {
    const updated = await simulationApi.disposeCriticalRisk(id, input);
    set({ selected: updated, runs: mergeRun(get().runs, updated) });
    return updated;
  },
}));
