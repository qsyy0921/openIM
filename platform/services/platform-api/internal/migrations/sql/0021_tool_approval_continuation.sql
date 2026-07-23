ALTER TABLE agent.tool_approvals
    ADD CONSTRAINT tool_approvals_tenant_id_id_unique UNIQUE (tenant_id, id);

ALTER TABLE agent.runs
    ADD COLUMN pending_tool_approval_id uuid,
    ADD CONSTRAINT runs_pending_tool_approval_fk
        FOREIGN KEY (tenant_id, pending_tool_approval_id)
        REFERENCES agent.tool_approvals(tenant_id, id),
    ADD CONSTRAINT runs_pending_tool_approval_state_check
        CHECK (pending_tool_approval_id IS NULL OR state = 'waiting_approval');

CREATE INDEX runs_pending_tool_approval_idx
    ON agent.runs (tenant_id, principal_member_id, pending_tool_approval_id)
    WHERE pending_tool_approval_id IS NOT NULL;

CREATE INDEX tool_approvals_requested_expiry_idx
    ON agent.tool_approvals (expires_at, id)
    WHERE state = 'requested';
