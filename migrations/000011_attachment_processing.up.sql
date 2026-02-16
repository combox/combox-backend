ALTER TABLE attachments
ADD COLUMN processing_status TEXT NOT NULL DEFAULT 'uploaded',
ADD COLUMN processing_error TEXT,
ADD COLUMN preview_object_key TEXT,
ADD COLUMN hls_master_object_key TEXT,
ADD COLUMN processed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_attachments_processing_status ON attachments(processing_status);
