import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Select, Table, message } from 'antd';
import { CheckCircle2, ShieldAlert } from 'lucide-react';
import type { ColumnsType } from 'antd/es/table';
import { ConfirmActionDialog } from '../components/common/ConfirmActionDialog';
import { PageHeader } from '../components/common/PageHeader';
import { RiskDispositionTable, dispositionActionLabel } from '../components/common/RiskDispositionTable';
import { RiskEvidenceTable } from '../components/common/RiskEvidenceTable';
import { StatusBadge } from '../components/common/StatusBadge';
import { RiskDispositionDialog } from '../components/simulation/RiskDispositionDialog';
import { useAuth } from '../hooks/useAuth';
import { useSimulationPolling } from '../hooks/useSimulationPolling';
import { useEdgeStore } from '../stores/edgeStore';
import { useSimulationStore } from '../stores/simulationStore';
import type { RiskEvidence } from '../types/simulation';
import { reportError } from '../utils/errors';
import { formatDateTime, formatNumber } from '../utils/format';

const riskKey = (risk: Pick<RiskEvidence, 'rule_code' | 'entity_type' | 'entity_id'>) => `${risk.rule_code}|${risk.entity_type}|${risk.entity_id}`;

export function InterlocksPage() {
  const { hasRole, user } = useAuth();
  const { edges, load: loadEdges } = useEdgeStore();
  const { runs, selected, select, confirm, dispose } = useSimulationStore();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [disposingRisk, setDisposingRisk] = useState<RiskEvidence | null>(null);
  const [disposing, setDisposing] = useState(false);
  useSimulationPolling(true, 9000);
  useEffect(() => { loadEdges().catch(reportError); }, [loadEdges]);
  const runsWithEvidence = useMemo(() => runs.filter((run) => (run.risk_flags_json?.length ?? 0) > 0), [runs]);
  useEffect(() => { if (!selected && runsWithEvidence[0]) void select(runsWithEvidence[0].id); }, [runsWithEvidence, select, selected]);
  const selectedEdgeIds = new Set((selected?.risk_flags_json ?? []).filter((risk) => risk.entity_type === 'airway_edge').map((risk) => risk.entity_id));
  const affectedEdges = edges.filter((edge) => selectedEdgeIds.has(edge.id));
  const dispositions = selected?.dispositions ?? [];
  const dispositionMap = useMemo(() => new Map(dispositions.map((item) => [riskKey(item), item])), [dispositions]);
  const criticalRisks = useMemo(() => (selected?.risk_flags_json ?? []).filter((risk) => risk.level === 'critical'), [selected]);
  const pendingCritical = criticalRisks.filter((risk) => !dispositionMap.has(riskKey(risk)));
  const allCriticalDisposed = pendingCritical.length === 0;
  const isInitiator = Boolean(user && selected && user.id === selected.started_by);
  const canDispose = hasRole('reviewer', 'admin') && !isInitiator;
  const candidateRuns = useMemo(() => {
    if (!selected) return [];
    return runs.filter((run) => run.started_at > selected.started_at || (run.started_at === selected.started_at && run.id > selected.id));
  }, [runs, selected]);
  const doConfirm = async () => {
    if (!selected) return;
    setBusy(true);
    try { await confirm(selected.id, note); message.success('风险证据已由当前复核人员确认'); setDialogOpen(false); setNote(''); } catch (error) { reportError(error, '风险确认未完成'); } finally { setBusy(false); }
  };
  const doDispose = async (payload: Parameters<typeof dispose>[1]) => {
    if (!selected) return;
    setDisposing(true);
    try { await dispose(selected.id, payload); message.success('严重风险处置已记录'); setDisposingRisk(null); } catch (error) { reportError(error, '风险处置未完成'); } finally { setDisposing(false); }
  };
  const criticalColumns: ColumnsType<RiskEvidence> = [
    { title: '规则', dataIndex: 'rule_code', width: 168, render: (value: string) => <code>{value}</code> },
    { title: '对象', width: 150, render: (_, row) => `${row.entity_type} #${row.entity_id || '-'}` },
    { title: '证据 / 阈值', width: 170, render: (_, row) => <span><strong>{formatNumber(row.evidence, 3)}</strong> / {formatNumber(row.threshold, 3)} {row.unit}</span> },
    {
      title: '处置状态', width: 240,
      render: (_, row) => {
        const record = dispositionMap.get(riskKey(row));
        return record
          ? <span>{dispositionActionLabel[record.action]} · {record.disposed_by_email}</span>
          : <strong>待处置</strong>;
      },
    },
    {
      title: '', width: 110,
      render: (_, row) => dispositionMap.has(riskKey(row)) ? null : (
        <Button size="small" type="primary" ghost disabled={!canDispose} onClick={() => setDisposingRisk(row)}>处置</Button>
      ),
    },
  ];
  return (
    <div className="page">
      <PageHeader eyebrow="规则引擎 / 人工确认" title="联锁风险证据" meta={<><span>{runsWithEvidence.length} 次运行触发规则</span><span>{edges.filter((edge) => edge.critical_path).length} 条关键路径</span><span>确认不等于现场执行授权</span></>} />
      <Alert className="section-alert" type="error" showIcon message="风险证据必须结合现场规程人工复核，系统不会下发任何控制命令" />
      <section className="interlock-selector"><label htmlFor="risk-run">推演记录</label><Select id="risk-run" value={selected?.id} onChange={(id) => select(id).catch(reportError)} options={runsWithEvidence.map((run) => ({ value: run.id, label: `#${run.id} · ${run.scenario?.name ?? `方案 ${run.scenario_id}`} · ${formatDateTime(run.started_at)}` }))} placeholder="暂无触发风险规则的运行" /></section>
      {selected ? <>
        <section className="risk-summary"><div><ShieldAlert size={23} /><span>规则触发</span><strong>{selected.risk_flags_json?.length ?? 0}</strong></div><div><span>严重待处置</span><strong>{pendingCritical.length}</strong></div><div><span>人工状态</span>{selected.risk_confirmed_at ? <StatusBadge status="confirmed" /> : <strong>待确认</strong>}</div><Button type="primary" icon={<CheckCircle2 size={17} />} disabled={Boolean(selected.risk_confirmed_at) || !allCriticalDisposed || !hasRole('reviewer', 'admin')} title={allCriticalDisposed ? undefined : '存在未逐条处置的严重风险，不能确认整次风险'} onClick={() => setDialogOpen(true)}>{selected.risk_confirmed_at ? '证据已确认' : '确认风险证据'}</Button></section>
        {!allCriticalDisposed && !selected.risk_confirmed_at ? <Alert className="section-alert" type="warning" showIcon message={`还有 ${pendingCritical.length} 条严重联锁风险未逐条处置，全部处置完成后才能确认整次风险`} /> : null}
        {isInitiator && !selected.risk_confirmed_at ? <Alert className="section-alert" type="info" showIcon message="复核员须不同于推演发起人，当前账号发起了本次推演，不能处置其严重风险" /> : null}
        <section className="workspace-section"><div className="section-heading"><div><span className="section-index">01</span><h2>规则、证据值与阈值</h2></div></div><RiskEvidenceTable risks={selected.risk_flags_json ?? []} /></section>
        <section className="workspace-section"><div className="section-heading"><div><span className="section-index">02</span><h2>严重风险逐条处置</h2></div></div><Table<RiskEvidence> rowKey={(row) => riskKey(row)} columns={criticalColumns} dataSource={criticalRisks} size="small" pagination={false} locale={{ emptyText: '本次运行没有严重等级风险，普通风险维持原确认流程' }} /></section>
        <section className="workspace-section"><div className="section-heading"><div><span className="section-index">03</span><h2>处置记录（处置人、依据）</h2></div></div><RiskDispositionTable dispositions={dispositions} /></section>
        <section className="workspace-section affected-list"><div className="section-heading"><div><span className="section-index">04</span><h2>受影响巷道</h2></div></div>{affectedEdges.length ? affectedEdges.map((edge) => <div key={edge.id}><strong>{edge.code}</strong><span>{edge.from_node?.code ?? edge.from_node_id} → {edge.to_node?.code ?? edge.to_node_id}</span><span>{edge.critical_path ? '关键路径' : '普通路径'}</span><span>风门：{edge.door_state}</span></div>) : <p className="muted">当前证据未直接关联巷道边。</p>}</section>
      </> : <div className="empty-state"><ShieldAlert size={28} /><h2>暂无风险证据</h2><p>完成已批准方案的推演后，触发的规则会出现在这里。</p></div>}
      <ConfirmActionDialog open={dialogOpen} title="确认已复核风险证据" consequence="此操作只记录你已查看当前证据，不会改变规则判定，也不会向现场设备发送指令。确认记录不可通过普通 API 删除。" confirmLabel="记录人工确认" noteLabel="复核说明" note={note} requireNote busy={busy} onNoteChange={setNote} onCancel={() => { setDialogOpen(false); setNote(''); }} onConfirm={() => void doConfirm()} />
      <RiskDispositionDialog open={Boolean(disposingRisk)} risk={disposingRisk} candidateRuns={candidateRuns} busy={disposing} onCancel={() => setDisposingRisk(null)} onSubmit={(payload) => void doDispose(payload)} />
    </div>
  );
}
