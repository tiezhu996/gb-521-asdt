import { Badge, Typography } from 'antd';
import { CheckCircle2, CornerUpLeft, Link2, ShieldCheck } from 'lucide-react';
import type { RiskDisposition, RiskDispositionDecision } from '../../types/simulation';
import { formatDateTime } from '../../utils/format';

export const dispositionDecisionMeta: Record<RiskDispositionDecision, { label: string; color: string; Icon: typeof CheckCircle2 }> = {
  accept_residual: { label: '接受剩余风险', color: 'warning', Icon: ShieldCheck },
  return_recalc: { label: '退回重算', color: 'processing', Icon: CornerUpLeft },
  link_followup: { label: '关联后续推演', color: 'success', Icon: Link2 },
};

interface Props {
  disposition: RiskDisposition;
  linkedRunLabel?: string;
}

export function RiskDispositionPanel({ disposition, linkedRunLabel }: Props) {
  const meta = dispositionDecisionMeta[disposition.decision] ?? { label: disposition.decision, color: 'default', Icon: CheckCircle2 };
  return (
    <div className="risk-disposition">
      <div className="risk-disposition-head">
        <Badge status="success" />
        <meta.Icon size={14} aria-hidden="true" />
        <strong>{meta.label}</strong>
        <span>{disposition.disposed_by_name}（{disposition.disposed_by_email}）</span>
        <time>{formatDateTime(disposition.created_at)}</time>
      </div>
      <div className="risk-disposition-body">
        <p><span>处置依据</span>{disposition.rationale}</p>
        {disposition.manual_authority && <p><span>人工授权依据</span>{disposition.manual_authority}</p>}
        {disposition.linked_run_id ? (
          <p><span>关联推演</span>{linkedRunLabel ? `#${disposition.linked_run_id} · ${linkedRunLabel}` : `#${disposition.linked_run_id}`}</p>
        ) : null}
      </div>
    </div>
  );
}

export function DispositionHint() {
  return <Typography.Text type="secondary">严重风险须由非发起人的复核员逐条处置后，才能确认整次风险。</Typography.Text>;
}
