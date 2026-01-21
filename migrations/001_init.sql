-- +goose Up

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE datasets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    source_file_url TEXT NOT NULL,
    row_count INT DEFAULT 0,
    status VARCHAR(50) DEFAULT 'uploaded',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE products (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    dataset_id UUID REFERENCES datasets(id) ON DELETE CASCADE,
    external_id VARCHAR(255),
    raw_data JSONB NOT NULL,
    current_data JSONB,
    version INT DEFAULT 1,
    status VARCHAR(50) DEFAULT 'pending',
    agent_readiness_score DECIMAL(3,2),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(dataset_id, external_id)
);

CREATE INDEX idx_products_dataset ON products(dataset_id);
CREATE INDEX idx_products_status ON products(status);

CREATE TABLE agent_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    product_id UUID REFERENCES products(id) ON DELETE CASCADE,
    goal TEXT NOT NULL,
    status VARCHAR(50) DEFAULT 'running',
    total_steps INT DEFAULT 0,
    tokens_used INT DEFAULT 0,
    started_at TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_sessions_product ON agent_sessions(product_id);

CREATE TABLE agent_traces (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id UUID REFERENCES agent_sessions(id) ON DELETE CASCADE,
    step_number INT NOT NULL,
    thought TEXT,
    tool_name VARCHAR(100),
    tool_input JSONB,
    tool_output JSONB,
    tokens_used INT DEFAULT 0,
    duration_ms INT DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_traces_session ON agent_traces(session_id, step_number);

CREATE TABLE proposals (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    product_id UUID REFERENCES products(id) ON DELETE CASCADE,
    session_id UUID REFERENCES agent_sessions(id),
    field VARCHAR(100) NOT NULL,
    before_value TEXT,
    after_value TEXT,
    rationale TEXT[],
    sources JSONB DEFAULT '[]',
    confidence DECIMAL(3,2),
    risk_level VARCHAR(20) DEFAULT 'medium',
    status VARCHAR(20) DEFAULT 'proposed',
    reviewed_by VARCHAR(255),
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_proposals_product ON proposals(product_id);
CREATE INDEX idx_proposals_status ON proposals(status);

CREATE TABLE rules (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    dataset_id UUID REFERENCES datasets(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(20) DEFAULT 'hard',
    field VARCHAR(100),
    condition JSONB NOT NULL,
    message TEXT,
    severity VARCHAR(20) DEFAULT 'error',
    active BOOLEAN DEFAULT true,
    created_by VARCHAR(50) DEFAULT 'system',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE jobs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    dataset_id UUID REFERENCES datasets(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL,
    status VARCHAR(50) DEFAULT 'pending',
    progress JSONB DEFAULT '{}',
    config JSONB DEFAULT '{}',
    error TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_jobs_dataset ON jobs(dataset_id);
CREATE INDEX idx_jobs_status ON jobs(status);

-- Insert default GMC rules
INSERT INTO rules (name, type, field, condition, message, severity, created_by) VALUES
    ('Title minimum length', 'hard', 'title', '{"operator": "min_length", "value": 30}', 'Title must be at least 30 characters', 'error', 'system'),
    ('Title maximum length', 'hard', 'title', '{"operator": "max_length", "value": 150}', 'Title must be at most 150 characters', 'error', 'system'),
    ('Description minimum length', 'hard', 'description', '{"operator": "min_length", "value": 50}', 'Description must be at least 50 characters', 'warning', 'system'),
    ('Image link required', 'hard', 'image_link', '{"operator": "required"}', 'Image link is required', 'error', 'system'),
    ('Price required', 'hard', 'price', '{"operator": "required"}', 'Price is required', 'error', 'system');

-- +goose Down

DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS rules;
DROP TABLE IF EXISTS proposals;
DROP TABLE IF EXISTS agent_traces;
DROP TABLE IF EXISTS agent_sessions;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS datasets;
