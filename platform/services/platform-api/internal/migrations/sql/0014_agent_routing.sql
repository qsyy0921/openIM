ALTER TABLE agent.runs
    ADD COLUMN route_status text CHECK (route_status IN ('selected', 'clarify', 'no_tool')),
    ADD COLUMN route_operation_id text,
    ADD COLUMN route_clarification text,
    ADD COLUMN route_provider_response_id text,
    ADD COLUMN route_router_version text,
    ADD COLUMN route_candidates jsonb,
    ADD COLUMN route_attempts integer NOT NULL DEFAULT 0 CHECK (route_attempts >= 0),
    ADD COLUMN model_attempts integer NOT NULL DEFAULT 0 CHECK (model_attempts >= 0),
    ADD COLUMN finalization_attempts integer NOT NULL DEFAULT 0 CHECK (finalization_attempts >= 0),
    ADD CONSTRAINT runs_route_shape_check CHECK (
        route_status IS NULL
        OR (
            route_provider_response_id IS NOT NULL
            AND route_router_version IS NOT NULL
            AND route_candidates IS NOT NULL
            AND jsonb_typeof(route_candidates) = 'array'
            AND (
                (route_status = 'selected' AND route_operation_id IS NOT NULL AND route_clarification IS NULL)
                OR (route_status = 'clarify' AND route_operation_id IS NULL AND route_clarification IS NOT NULL)
                OR (route_status = 'no_tool' AND route_operation_id IS NULL AND route_clarification IS NULL)
            )
        )
    );
