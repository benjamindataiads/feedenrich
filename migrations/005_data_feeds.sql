-- +goose Up
-- Migration: Data Feeds, Execution tracking, and Approval Rules

-- Dataset versions (import history)
CREATE TABLE IF NOT EXISTS dataset_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    version_number INT NOT NULL,
    file_name VARCHAR(255),
    row_count INT,
    created_at TIMESTAMP DEFAULT NOW(),
    created_by VARCHAR(255),
    notes TEXT,
    UNIQUE(dataset_id, version_number)
);

CREATE INDEX idx_dataset_versions_dataset ON dataset_versions(dataset_id);

-- Snapshots (state before/after operations)
CREATE TABLE IF NOT EXISTS dataset_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    snapshot_type VARCHAR(50) NOT NULL, -- 'pre_enrichment', 'post_enrichment', 'manual'
    product_count INT,
    created_at TIMESTAMP DEFAULT NOW(),
    created_by VARCHAR(255)
);

CREATE INDEX idx_snapshots_dataset ON dataset_snapshots(dataset_id);

-- Snapshot data (separate table for large data)
CREATE TABLE IF NOT EXISTS snapshot_products (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    snapshot_id UUID NOT NULL REFERENCES dataset_snapshots(id) ON DELETE CASCADE,
    product_id UUID NOT NULL,
    raw_data JSONB NOT NULL,
    current_data JSONB
);

CREATE INDEX idx_snapshot_products_snapshot ON snapshot_products(snapshot_id);

-- Change log (audit trail)
CREATE TABLE IF NOT EXISTS change_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID REFERENCES datasets(id) ON DELETE SET NULL,
    product_id UUID REFERENCES products(id) ON DELETE SET NULL,
    action VARCHAR(50) NOT NULL, -- 'import', 'proposal_accepted', 'proposal_rejected', 'manual_edit', 'export', 'restore'
    field VARCHAR(100),
    old_value TEXT,
    new_value TEXT,
    source VARCHAR(50), -- 'user', 'agent', 'rule'
    module VARCHAR(100), -- optimization module if applicable
    created_at TIMESTAMP DEFAULT NOW(),
    created_by VARCHAR(255)
);

CREATE INDEX idx_changelog_dataset ON change_log(dataset_id);
CREATE INDEX idx_changelog_product ON change_log(product_id);
CREATE INDEX idx_changelog_created ON change_log(created_at);

-- Auto-approval rules
CREATE TABLE IF NOT EXISTS approval_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id UUID REFERENCES datasets(id) ON DELETE CASCADE, -- null = global
    name VARCHAR(255) NOT NULL,
    field VARCHAR(100), -- null = all fields
    module VARCHAR(100), -- null = all modules
    min_confidence DECIMAL(3,2),
    max_risk VARCHAR(20), -- 'low', 'medium', 'high'
    action VARCHAR(20) NOT NULL, -- 'auto_approve', 'auto_reject', 'flag'
    priority INT DEFAULT 0, -- higher = evaluated first
    active BOOLEAN DEFAULT true,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP
);

CREATE INDEX idx_approval_rules_dataset ON approval_rules(dataset_id);
CREATE INDEX idx_approval_rules_active ON approval_rules(active);

-- Enhance jobs table for execution tracking
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS module VARCHAR(100);
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS total_items INT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS processed_items INT DEFAULT 0;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS proposals_generated INT DEFAULT 0;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS logs JSONB DEFAULT '[]';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_jobs_status_dataset ON jobs(status, dataset_id);
CREATE INDEX IF NOT EXISTS idx_jobs_updated ON jobs(updated_at);

-- Add module field to proposals for grouping
ALTER TABLE proposals ADD COLUMN IF NOT EXISTS module VARCHAR(100);

-- Add current version to datasets
ALTER TABLE datasets ADD COLUMN IF NOT EXISTS current_version INT DEFAULT 1;

-- +goose Down
DROP TABLE IF EXISTS snapshot_products;
DROP TABLE IF EXISTS dataset_snapshots;
DROP TABLE IF EXISTS dataset_versions;
DROP TABLE IF EXISTS change_log;
DROP TABLE IF EXISTS approval_rules;

ALTER TABLE jobs DROP COLUMN IF EXISTS module;
ALTER TABLE jobs DROP COLUMN IF EXISTS total_items;
ALTER TABLE jobs DROP COLUMN IF EXISTS processed_items;
ALTER TABLE jobs DROP COLUMN IF EXISTS proposals_generated;
ALTER TABLE jobs DROP COLUMN IF EXISTS logs;
ALTER TABLE jobs DROP COLUMN IF EXISTS updated_at;

ALTER TABLE proposals DROP COLUMN IF EXISTS module;
ALTER TABLE datasets DROP COLUMN IF EXISTS current_version;
