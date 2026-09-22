import { useEffect, useMemo, useState } from 'react';
import { Alert, Button, Select, Tag, message } from 'antd';
import { CheckCircle2, ShieldAlert } from 'lucide-react';
import { ConfirmActionDialog } from '../components/common/ConfirmActionDialog';
import { PageHeader } from '../components/common/PageHeader';
import { CriticalRiskSummary, RiskEvidenceTable } from '../components/common/RiskEvidenceTable';
import { RiskDispositionDialog } from '../components/common/RiskDispositionDialog';
import { StatusBadge } from '../components/common/StatusBadge';
import { useAuth } from '../hooks/useAuth';
import { useSimulationPolling } from '../hooks/useSimulationPolling';
import { useEdgeStore } from '../stores/edgeStore';
import { useSimulationStore } from '../stores/simulationStore';
import type { RiskEvidence } from '../types/simulation';
import { makeRiskKey } from '../types/simulation';
import { reportError } from '../utils/errors';
import { formatDateTime } from '../utils/format';

export function InterlocksPage() {
  const { user, hasRole } = useAuth();
  const { edges, load: loadEdges } = useEdgeStore();
  const { runs, selected, select, confirm, dispose } = useSimulationStore();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [disposeRisk, setDisposeRisk] = useState<RiskEvidence | null>(null);
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [disposeBusy, setDisposeBusy] = useState(false);
  useSimulationPolling(true, 9000);
  useEffect(() => { loadEdges().catch(reportError); }, [loadEdges]);
  const runsWithEvidence = useMemo(() => runs.filter((run) => (run.risk_flags_json?.length ?? 0) > 0), [runs]);
  useEffect(() => { if (!selected && runsWithEvidence[0]) void select(runsWithEvidence[0].id); }, [runsWithEvidence, select, selected]);

  const runsById = useMemo(() => new Map(runs.map((run) => [run.id, run])), [runs]);
  const selectedEdgeIds = new Set((selected?.risk_flags_json ?? []).filter((risk) => risk.entity_type === 'airway_edge').map((risk) => risk.entity_id));
  const affectedEdges = edges.filter((edge) => selectedEdgeIds.has(edge.id));

  const canReview = hasRole('reviewer', 'admin');
  const isInitiator = Boolean(user && selected && user.id === selected.started_by);
  const confirmed = Boolean(selected?.risk_confirmed_at);
  const criticalRisks = (selected?.risk_flags_json ?? []).filter((risk) => risk.level === 'critical');
  const disposedKeys = new Set((selected?.risk_dispositions ?? []).map((item) => item.risk_key));
  const pendingCritical = criticalRisks.filter((risk) => !disposedKeys.has(makeRiskKey(risk)));
  const canDispose = canReview && !isInitiator && !confirmed && Boolean(selected && !['queued', 'running'].includes(selected.run_status));

  const doConfirm = async () => {
    if (!selected) return;
    setBusy(true);
    try {
      await confirm(selected.id, note);
      message.success('整次风险已由当前复核人员确认');
      setConfirmOpen(false);
      setNote('');
    } catch (error) { reportError(error, '整次风险确认未完成'); } finally { setBusy(false); }
  };

  const doDispose = async (input: Parameters<typeof dispose>[1]) => {
    if (!selected) return;
    setDisposeBusy(true);
    try {
      await dispose(selected.id, input);
      message.success('该条严重风险处置已记录（重复提交不会覆盖原证据）');
      setDisposeRisk(null);
    } catch (error) { reportError(error, '严重风险处置未完成'); } finally { setDisposeBusy(false); }
  };

  const confirmBlockedReason = !selected ? ''
    : isInitiator ? '复核员必须不同于推演发起人，发起人本人不能确认本次风险'
      : pendingCritical.length > 0 ? `还有 ${pendingCritical.length} 条严重联锁风险未逐条处置`
        : '';

  return (
    <div className="page">
      <PageHeader eyebrow="规则引擎 / 人工确认" title="联锁风险证据" meta={<><span>{runsWithEvidence.length} 次运行触发规则</span><span>{edges.filter((edge) => edge.critical_path).length} 条关键路径</span><span>确认不等于现场执行授权</span></>} />
      <Alert className="section-alert" type="error" showIcon message="严重风险须逐条处置（接受剩余风险/退回重算/关联后续推演）并写明依据，全部处置后才能确认整次风险；系统不会下发任何控制命令" />
      <section className="interlock-selector"><label htmlFor="risk-run">推演记录</label><Select id="risk-run" value={selected?.id} onChange={(id) => select(id).catch(reportError)} options={runsWithEvidence.map((run) => ({ value: run.id, label: `#${run.id} · ${run.scenario?.name ?? `方案 ${run.scenario_id}`} · ${formatDateTime(run.started_at)}` }))} placeholder="暂无触发风险规则的运行" /></section>
      {selected ? <>
        <section className="risk-summary">
          <div><ShieldAlert size={23} /><span>规则触发</span><strong>{selected.risk_flags_json?.length ?? 0}</strong></div>
          <div><span>受影响巷道</span><strong>{affectedEdges.length}</strong></div>
          <div>
            <span>人工状态</span>
            {confirmed ? <StatusBadge status="confirmed" /> : <><strong>待确认</strong>{pendingCritical.length > 0 && <Tag color="error">严重风险 {pendingCritical.length} 条待处置</Tag>}</>}
          </div>
          <Button type="primary" icon={<CheckCircle2 size={17} />} disabled={confirmed || !canReview || isInitiator || pendingCritical.length > 0} onClick={() => setConfirmOpen(true)}>{confirmed ? '整次风险已确认' : '确认整次风险'}</Button>
        </section>
        {!confirmed && canReview && isInitiator && <Alert className="section-alert" type="warning" showIcon message="你是本次推演的发起人，按职责分离要求不能由本人处置或确认，请由其他复核员操作。" />}
        {!confirmed && confirmBlockedReason && !isInitiator && <Alert className="section-alert" type="warning" showIcon message={confirmBlockedReason} />}
        <CriticalRiskSummary run={selected} />
        <section className="workspace-section">
          <div className="section-heading"><div><span className="section-index">01</span><h2>规则、证据值与阈值（严重风险可展开逐条处置）</h2></div></div>
          <RiskEvidenceTable risks={selected.risk_flags_json ?? []} run={selected} runsById={runsById} canDispose={canDispose} onDispose={setDisposeRisk} />
        </section>
        {confirmed && (
          <section className="workspace-section confirmation-record">
            <div className="section-heading"><div><span className="section-index">02</span><h2>整次确认记录</h2></div><StatusBadge status="confirmed" /></div>
            <p><span>确认人</span><strong>{selected.risk_confirmed_by_name || `用户 #${selected.risk_confirmed_by}`}</strong>{selected.risk_confirmed_by_email && <em>{selected.risk_confirmed_by_email}</em>}</p>
            <p><span>确认时间</span>{selected.risk_confirmed_at ? formatDateTime(selected.risk_confirmed_at) : '-'}</p>
            <p><span>复核说明</span>{selected.confirmation_note}</p>
          </section>
        )}
        <section className="workspace-section affected-list">
          <div className="section-heading"><div><span className="section-index">{confirmed ? '03' : '02'}</span><h2>受影响巷道</h2></div></div>
          {affectedEdges.length ? affectedEdges.map((edge) => <div key={edge.id}><strong>{edge.code}</strong><span>{edge.from_node?.code ?? edge.from_node_id} → {edge.to_node?.code ?? edge.to_node_id}</span><span>{edge.critical_path ? '关键路径' : '普通路径'}</span><span>风门：{edge.door_state}</span></div>) : <p className="muted">当前证据未直接关联巷道边。</p>}
        </section>
      </> : <div className="empty-state"><ShieldAlert size={28} /><h2>暂无风险证据</h2><p>完成已批准方案的推演后，触发的规则会出现在这里。</p></div>}
      {selected && <RiskDispositionDialog open={Boolean(disposeRisk)} run={selected} risk={disposeRisk} followupRuns={runs} busy={disposeBusy} onCancel={() => setDisposeRisk(null)} onSubmit={doDispose} />}
      <ConfirmActionDialog open={confirmOpen} title="确认整次风险证据" consequence="所有严重风险均已逐条处置。此操作记录你已复核全部证据，不会改变规则判定，也不会向现场设备发送指令；确认记录不可通过普通 API 删除。" confirmLabel="记录人工整次确认" noteLabel="整次复核说明" note={note} requireNote busy={busy} onNoteChange={setNote} onCancel={() => { setConfirmOpen(false); setNote(''); }} onConfirm={() => void doConfirm()} />
    </div>
  );
}
