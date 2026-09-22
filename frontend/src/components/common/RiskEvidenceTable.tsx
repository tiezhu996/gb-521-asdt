import { Button, Table, Tag, Typography } from 'antd';
import { AlertOctagon, AlertTriangle, Info } from 'lucide-react';
import type { ColumnsType } from 'antd/es/table';
import type { RiskEvidence, SimulationRun } from '../../types/simulation';
import { makeRiskKey } from '../../types/simulation';
import { formatNumber } from '../../utils/format';
import { RiskDispositionPanel, dispositionDecisionMeta } from './RiskDispositionPanel';

const levelConfig = {
  critical: { label: '严重', Icon: AlertOctagon, className: 'risk-critical' },
  warning: { label: '警告', Icon: AlertTriangle, className: 'risk-warning' },
  info: { label: '提示', Icon: Info, className: 'risk-info' },
};

interface Props {
  risks: RiskEvidence[];
  loading?: boolean;
  run?: SimulationRun;
  runsById?: Map<number, SimulationRun>;
  canDispose?: boolean;
  onDispose?(risk: RiskEvidence): void;
}

export function RiskEvidenceTable({ risks, loading = false, run, runsById, canDispose = false, onDispose }: Props) {
  const dispositionByKey = new Map((run?.risk_dispositions ?? []).map((item) => [item.risk_key, item]));
  const columns: ColumnsType<RiskEvidence> = [
    {
      title: '等级', dataIndex: 'level', width: 105,
      render: (level: RiskEvidence['level']) => {
        const config = levelConfig[level];
        return <span className={`risk-level ${config.className}`}><config.Icon size={15} />{config.label}</span>;
      },
    },
    { title: '规则', dataIndex: 'rule_code', width: 168, render: (value: string) => <code>{value}</code> },
    { title: '对象', width: 150, render: (_, row) => `${row.entity_type} #${row.entity_id || '-'}` },
    { title: '证据', width: 150, render: (_, row) => <strong>{formatNumber(row.evidence, 3)} {row.unit}</strong> },
    { title: '阈值', width: 150, render: (_, row) => `${formatNumber(row.threshold, 3)} ${row.unit}` },
    { title: '判定说明', dataIndex: 'description', width: 260 },
  ];
  return (
    <Table<RiskEvidence>
      rowKey={(row) => makeRiskKey(row)}
      columns={columns}
      dataSource={risks}
      loading={loading}
      size="small"
      pagination={false}
      scroll={{ x: 900 }}
      locale={{ emptyText: <Typography.Text type="secondary">当前结果未触发联锁风险规则</Typography.Text> }}
      expandable={{
        rowExpandable: (risk) => risk.level === 'critical' && Boolean(run),
        expandedRowRender: (risk) => {
          const disposition = dispositionByKey.get(makeRiskKey(risk));
          if (!disposition) {
            return (
              <div className="risk-disposition-pending">
                <Tag color="error">待逐条处置</Tag>
                <Typography.Text type="secondary">该条严重风险尚未处置，处置并写明依据前不得确认整次风险。</Typography.Text>
                {canDispose && onDispose && (
                  <Button size="small" type="primary" ghost onClick={() => onDispose(risk)}>逐条处置</Button>
                )}
              </div>
            );
          }
          const linked = disposition.linked_run_id ? runsById?.get(disposition.linked_run_id) : undefined;
          return <RiskDispositionPanel disposition={disposition} linkedRunLabel={linked?.scenario?.name} />;
        },
        showExpandColumn: true,
      }}
    />
  );
}

export function CriticalRiskSummary({ run }: { run: SimulationRun }) {
  const critical = (run.risk_flags_json ?? []).filter((risk) => risk.level === 'critical');
  if (critical.length === 0) return null;
  const keys = new Set((run.risk_dispositions ?? []).map((item) => item.risk_key));
  const pending = critical.filter((risk) => !keys.has(makeRiskKey(risk))).length;
  const decisionCounts = (run.risk_dispositions ?? []).reduce<Record<string, number>>((acc, item) => {
    acc[item.decision] = (acc[item.decision] ?? 0) + 1;
    return acc;
  }, {});
  return (
    <div className="critical-summary">
      <span><strong>{critical.length}</strong> 条严重风险</span>
      <span className={pending > 0 ? 'critical-pending' : 'critical-done'}><strong>{critical.length - pending}</strong>/{critical.length} 已逐条处置</span>
      {Object.entries(decisionCounts).map(([decision, count]) => {
        const meta = dispositionDecisionMeta[decision as keyof typeof dispositionDecisionMeta];
        return <span key={decision}><meta.Icon size={13} />{meta.label} × {count}</span>;
      })}
      {pending > 0 && <Tag color="error">剩余 {pending} 条未处置，整次确认已锁定</Tag>}
    </div>
  );
}
