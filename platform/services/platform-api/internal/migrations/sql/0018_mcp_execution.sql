ALTER TABLE capability.tool_descriptors
    ADD COLUMN source_operation text;

ALTER TABLE capability.tool_descriptors DISABLE TRIGGER tool_descriptors_immutable_update;
UPDATE capability.tool_descriptors SET source_operation = operation_id;
ALTER TABLE capability.tool_descriptors ENABLE TRIGGER tool_descriptors_immutable_update;

ALTER TABLE capability.tool_descriptors
    ALTER COLUMN source_operation SET NOT NULL,
    ADD CONSTRAINT tool_descriptors_source_operation_check
        CHECK (length(source_operation) BETWEEN 1 AND 256);
