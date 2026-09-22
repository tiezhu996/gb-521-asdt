import { useEffect, useMemo, useState } from 'react';
import { Input, Modal, Radio, Select, Typography } from 'antd';
import type { DisposeRiskPayload } from '../../api/simulations';
import type { DispositionAction, RiskEvidence, SimulationRun } from '../../types/simulation';
import { dispositionActionLabel } from '../common/RiskDispositionTable';
import { formatDateTime, formatNumber } from '../../utils/format';

interface Props {
  open: boolean;
  risk: RiskEvidence | null;
  candidateRuns: SimulationRun[];
  busy?: boolean;
  onCancel(): void;
  onSubmit(payload: DisposeRiskPayload): void;
}

const finishedStatuses = new Set(['converged', 'not_converged', 'invalid_input', 'failed']);

export function RiskDispositionDialog({ open, risk, candidateRuns, busy = false, onCancel, onSubmit }: Props) {
  const [action, setAction] = useState<DispositionAction>('recalculate');
  const [rationale, setRationale] = useState('');
  const [authorizationRef, setAuthorizationRef] = useState('');
  const [linkedRunId, setLinkedRunId] = useState<number>();
  useEffect(() => {
    if (open) {
      setAction('recalculate');
      setRationale('');
      setAuthorizationRef('');
      setLinkedRunId(undefined);
    }
  }, [open, risk]);
  const linkOptions = useMemo(
    () => candidateRuns
      .filter((run) => finishedStatuses.has(run.run_status))
      .map((run) => ({ value: run.id, label: `#${run.id} · ${run.scenario?.name ?? `方案 ${run.scenario_id}`} · ${formatDateTime(run.started_at)}` })),
    [candidateRuns],
  );
  const valid = rationale.trim().length >= 4
    && (action !== 'accept' || authorizationRef.trim().length >= 4)
    && (action !== 'link_simulation' || Boolean(linkedRunId));
  const submit = () => {
    if (!risk || !valid) return;
    const payload: DisposeRiskPayload = {
      rule_code: risk.rule_code,
      entity_type: risk.entity_type,
      entity_id: risk.entity_id,
      action,
      rationale: rationale.trim(),
    };
    if (action === 'accept') payload.authorization_ref = authorizationRef.trim();
    if (action === 'link_simulation') payload.linked_run_id = linkedRunId;
    onSubmit(payload);
  };
  return (
    <Modal
      open={open}
      title="逐条处置严重联锁风险"
      okText="记录处置"
      cancelText="返回检查"
      okButtonProps={{ disabled: !valid, loading: busy }}
      onOk={submit}
      onCancel={onCancel}
      destroyOnClose
    >
      {risk ? (
        <Typography.Paragraph className="dialog-consequence">
          <code>{risk.rule_code}</code> · {risk.entity_type} #{risk.entity_id} · 证据 {formatNumber(risk.evidence, 3)} {risk.unit}（阈值 {formatNumber(risk.threshold, 3)} {risk.unit}）。处置记录写入后不可通过普通 API 修改或覆盖，重复提交只会保留第一条。
        </Typography.Paragraph>
      ) : null}
      <label className="field-label">处置方式（必选）</label>
      <Radio.Group
        value={action}
        onChange={(event) => setAction(event.target.value as DispositionAction)}
        options={(['accept', 'recalculate', 'link_simulation'] as DispositionAction[]).map((value) => ({ value, label: dispositionActionLabel[value] }))}
      />
      {action === 'accept' ? (
        <>
          <label className="field-label" htmlFor="authorization-ref">人工授权依据（接受严重风险必填）</label>
          <Input id="authorization-ref" value={authorizationRef} maxLength={200} placeholder="例如：矿总工程师批准单编号" onChange={(event) => setAuthorizationRef(event.target.value)} />
        </>
      ) : null}
      {action === 'link_simulation' ? (
        <>
          <label className="field-label" htmlFor="linked-run">关联后续已完成推演（方案版本与网络快照须与当前一致，且同一规则不再触发）</label>
          <Select
            id="linked-run"
            style={{ width: '100%' }}
            value={linkedRunId}
            options={linkOptions}
            placeholder="选择当前推演之后已完成的运行"
            onChange={(value: number) => setLinkedRunId(value)}
          />
        </>
      ) : null}
      <label className="field-label" htmlFor="disposition-rationale">处置依据（必填，至少 4 个字符）</label>
      <Input.TextArea id="disposition-rationale" value={rationale} rows={4} maxLength={500} showCount onChange={(event) => setRationale(event.target.value)} />
    </Modal>
  );
}
