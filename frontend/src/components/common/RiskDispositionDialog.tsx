import { useEffect, useMemo, useState } from 'react';
import { Alert, Input, Modal, Radio, Select, Typography } from 'antd';
import { Link2 } from 'lucide-react';
import type { DisposeRiskInput } from '../../api/simulations';
import type { RiskEvidence, RiskDispositionDecision, SimulationRun } from '../../types/simulation';
import { finishedSimulationStatuses, makeRiskKey } from '../../types/simulation';
import { formatDateTime } from '../../utils/format';

const decisionOptions: { value: RiskDispositionDecision; label: string; hint: string }[] = [
  { value: 'accept_residual', label: '接受剩余风险', hint: '保留本次判定并接受残余风险，必须填写人工授权依据。' },
  { value: 'return_recalc', label: '退回重算', hint: '要求工程师调整方案或网络后重新发起推演。' },
  { value: 'link_followup', label: '关联后续已完成推演', hint: '关联同方案版本、同网络快照且同一规则不再触发的后续推演。' },
];

interface Props {
  open: boolean;
  run: SimulationRun;
  risk: RiskEvidence | null;
  followupRuns: SimulationRun[];
  busy: boolean;
  onCancel(): void;
  onSubmit(input: DisposeRiskInput): Promise<void>;
}

export function RiskDispositionDialog({ open, run, risk, followupRuns, busy, onCancel, onSubmit }: Props) {
  const [decision, setDecision] = useState<RiskDispositionDecision>('return_recalc');
  const [rationale, setRationale] = useState('');
  const [manualAuthority, setManualAuthority] = useState('');
  const [linkedRunId, setLinkedRunId] = useState<number>();

  useEffect(() => {
    if (open) {
      setDecision('return_recalc');
      setRationale('');
      setManualAuthority('');
      setLinkedRunId(undefined);
    }
  }, [open, risk]);

  const followupKey = risk ? makeRiskKey(risk) : '';
  const eligibleRuns = useMemo(() => followupRuns.filter((item) => {
    if (item.id <= run.id || item.scenario_id !== run.scenario_id) return false;
    if (!finishedSimulationStatuses.includes(item.run_status)) return false;
    return !(item.risk_flags_json ?? []).some((flag) => makeRiskKey(flag) === followupKey);
  }), [followupRuns, run.id, run.scenario_id, followupKey]);

  const rationaleValid = rationale.trim().length >= 4;
  const authorityValid = decision !== 'accept_residual' || manualAuthority.trim().length > 0;
  const linkValid = decision !== 'link_followup' || Boolean(linkedRunId);
  const canSubmit = rationaleValid && authorityValid && linkValid;

  const submit = async () => {
    if (!risk || !canSubmit) return;
    await onSubmit({
      risk_key: makeRiskKey(risk),
      decision,
      rationale: rationale.trim(),
      ...(decision === 'accept_residual' ? { manual_authority: manualAuthority.trim() } : {}),
      ...(decision === 'link_followup' && linkedRunId ? { linked_run_id: linkedRunId } : {}),
    });
  };

  const activeHint = decisionOptions.find((item) => item.value === decision)?.hint;

  return (
    <Modal
      open={open}
      title={`逐条处置严重风险 · ${risk?.rule_code ?? ''}`}
      okText="提交本条处置"
      cancelText="取消"
      okButtonProps={{ disabled: !canSubmit, loading: busy }}
      onOk={() => void submit()}
      onCancel={onCancel}
      destroyOnClose
      width={640}
    >
      {risk && (
        <Typography.Paragraph className="dialog-consequence">
          对象：{risk.entity_type} #{risk.entity_id} ｜ 证据值 {risk.evidence} {risk.unit} ｜ 阈值 {risk.threshold} {risk.unit}
          <br />{risk.description}。处置只形成审计证据，不会向现场设备下发任何指令。
        </Typography.Paragraph>
      )}
      <div className="dispose-field">
        <label className="field-label">处置决定（必选）</label>
        <Radio.Group value={decision} onChange={(event) => setDecision(event.target.value)}>
          <div className="dispose-decisions">
            {decisionOptions.map((option) => (
              <Radio key={option.value} value={option.value} disabled={busy}>
                <strong>{option.label}</strong>
                <span>{option.hint}</span>
              </Radio>
            ))}
          </div>
        </Radio.Group>
        <Typography.Text type="secondary">{activeHint}</Typography.Text>
      </div>
      {decision === 'link_followup' && (
        <div className="dispose-field">
          <label className="field-label" htmlFor="linked-run">后续已完成推演（同方案版本、同网络快照且该规则不再触发）</label>
          <Select
            id="linked-run"
            className="dispose-linked-select"
            value={linkedRunId}
            onChange={setLinkedRunId}
            disabled={busy}
            placeholder="选择后续推演"
            options={eligibleRuns.map((item) => ({
              value: item.id,
              label: `#${item.id} · ${item.scenario?.name ?? `方案 ${item.scenario_id}`} · ${formatDateTime(item.started_at)} · ${item.run_status}`,
            }))}
            notFoundContent="没有满足同版本、同快照且规则不再触发的后续完成推演"
            suffixIcon={<Link2 size={14} />}
          />
        </div>
      )}
      <div className="dispose-field">
        <label className="field-label" htmlFor="dispose-rationale">处置依据（必填，至少 4 个字符）</label>
        <Input.TextArea id="dispose-rationale" value={rationale} rows={3} maxLength={500} showCount disabled={busy} onChange={(event) => setRationale(event.target.value)} />
      </div>
      {decision === 'accept_residual' && (
        <div className="dispose-field">
          <Alert type="warning" showIcon message="接受严重剩余风险必须填写人工授权依据（授权人、授权文件或作业许可编号）" />
          <label className="field-label" htmlFor="manual-authority">人工授权依据（必填）</label>
          <Input.TextArea id="manual-authority" value={manualAuthority} rows={2} maxLength={500} showCount disabled={busy} onChange={(event) => setManualAuthority(event.target.value)} />
        </div>
      )}
    </Modal>
  );
}
