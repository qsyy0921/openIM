ALTER TABLE agent.runs
    ADD COLUMN tool_arguments jsonb,
    ADD COLUMN tool_plan_provider_response_id text,
    ADD COLUMN tool_result jsonb,
    ADD COLUMN tool_plan_attempts integer NOT NULL DEFAULT 0 CHECK (tool_plan_attempts >= 0),
    ADD COLUMN tool_execution_attempts integer NOT NULL DEFAULT 0 CHECK (tool_execution_attempts >= 0),
    ADD CONSTRAINT runs_tool_plan_shape_check CHECK (
        (tool_arguments IS NULL AND tool_plan_provider_response_id IS NULL)
        OR (
            tool_arguments IS NOT NULL AND jsonb_typeof(tool_arguments) = 'object'
            AND tool_plan_provider_response_id IS NOT NULL
        )
    );
