import { Table, Typography } from 'antd';
import { CheckCircle2, Link2, RotateCcw } from 'lucide-react';
import type { ColumnsType } from 'antd/es/table';
import type { DispositionAction, RiskDisposition } from '../../types/simulation';
import { formatDateTime } from '../../utils/format';

export const dispositionActionLabel: Record<DispositionAction, string> = {
  accept: '接受剩余风险',
  recalculate: '退回重算',
  link_simulation: '关联后续推演',
};

const actionIcons = {
  accept: CheckCircle2,
  recalculate: RotateCcw,
  link_simulation: Link2,
} as const;

export function RiskDispositionTable({ dispositions, loading = false }: { dispositions: RiskDisposition[]; loading?: boolean }) {
  const columns: ColumnsType<RiskDisposition> = [
    {
      title: '处置方式', dataIndex: 'action', width: 150,
      render: (action: DispositionAction) => {
        const Icon = actionIcons[action];
        return <span className="risk-level risk-info"><Icon size={15} />{dispositionActionLabel[action]}</span>;
      },
    },
    { title: '规则', dataIndex: 'rule_code', width: 168, render: (value: string) => <code>{value}</code> },
    { title: '对象', width: 150, render: (_, row) => `${row.entity_type} #${row.entity_id || '-'}` },
    { title: '处置人', dataIndex: 'disposed_by_email', width: 190 },
    { title: '处置时间', dataIndex: 'disposed_at', width: 170, render: formatDateTime },
    {
      title: '依据', width: 320,
      render: (_, row) => (
        <span>
          {row.rationale}
          {row.authorization_ref ? <Typography.Text type="secondary" style={{ display: 'block' }}>人工授权依据：{row.authorization_ref}</Typography.Text> : null}
          {row.linked_run_id ? <Typography.Text type="secondary" style={{ display: 'block' }}>关联推演：#{row.linked_run_id}</Typography.Text> : null}
        </span>
      ),
    },
  ];
  return (
    <Table<RiskDisposition>
      rowKey="id"
      columns={columns}
      dataSource={dispositions}
      loading={loading}
      size="small"
      pagination={false}
      scroll={{ x: 1000 }}
      locale={{ emptyText: <Typography.Text type="secondary">尚无严重风险处置记录</Typography.Text> }}
    />
  );
}
