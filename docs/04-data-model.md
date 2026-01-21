# Data Model (PostgreSQL)

## Tables principales

### datasets
```sql
CREATE TABLE datasets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) NOT NULL,
  source_file_url TEXT NOT NULL,
  row_count INT,
  status VARCHAR(50) DEFAULT 'uploaded',
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);
```

### products
```sql
CREATE TABLE products (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  dataset_id UUID REFERENCES datasets(id) ON DELETE CASCADE,
  external_id VARCHAR(255),
  raw_data JSONB NOT NULL,
  current_data JSONB,
  version INT DEFAULT 1,
  status VARCHAR(50) DEFAULT 'pending', -- pending, processing, enriched, needs_review
  agent_readiness_score DECIMAL(3,2),
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(dataset_id, external_id)
);
```

### agent_sessions
```sql
CREATE TABLE agent_sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id UUID REFERENCES products(id) ON DELETE CASCADE,
  goal TEXT NOT NULL,
  status VARCHAR(50) DEFAULT 'running', -- running, completed, failed, paused
  total_steps INT DEFAULT 0,
  tokens_used INT DEFAULT 0,
  started_at TIMESTAMPTZ DEFAULT NOW(),
  completed_at TIMESTAMPTZ
);
```

### agent_traces
```sql
CREATE TABLE agent_traces (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id UUID REFERENCES agent_sessions(id) ON DELETE CASCADE,
  step_number INT NOT NULL,
  thought TEXT,                          -- raisonnement de l'agent
  tool_name VARCHAR(100),
  tool_input JSONB,
  tool_output JSONB,
  tokens_used INT,
  duration_ms INT,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_traces_session ON agent_traces(session_id, step_number);
```

### proposals
```sql
CREATE TABLE proposals (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id UUID REFERENCES products(id) ON DELETE CASCADE,
  session_id UUID REFERENCES agent_sessions(id),
  field VARCHAR(100) NOT NULL,
  before_value TEXT,
  after_value TEXT,
  rationale TEXT[],
  sources JSONB DEFAULT '[]',
  confidence DECIMAL(3,2),
  risk_level VARCHAR(20) DEFAULT 'medium',
  status VARCHAR(20) DEFAULT 'proposed', -- proposed, accepted, rejected, edited
  reviewed_by VARCHAR(255),
  reviewed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```

### sources
```sql
CREATE TABLE sources (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  proposal_id UUID REFERENCES proposals(id) ON DELETE CASCADE,
  type VARCHAR(50) NOT NULL,             -- feed, web, vision
  reference TEXT NOT NULL,               -- URL ou champ source
  evidence TEXT,                         -- snippet ou observation
  confidence DECIMAL(3,2),
  fetched_at TIMESTAMPTZ DEFAULT NOW()
);
```

### rules
```sql
CREATE TABLE rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  dataset_id UUID REFERENCES datasets(id),  -- NULL = global
  name VARCHAR(255) NOT NULL,
  type VARCHAR(20) DEFAULT 'hard',
  field VARCHAR(100),
  condition JSONB NOT NULL,
  message TEXT,
  severity VARCHAR(20) DEFAULT 'error',
  active BOOLEAN DEFAULT true,
  created_by VARCHAR(50) DEFAULT 'system', -- system, agent, user
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```

### jobs
```sql
CREATE TABLE jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  dataset_id UUID REFERENCES datasets(id) ON DELETE CASCADE,
  type VARCHAR(50) NOT NULL,             -- enrich_all, enrich_batch, single_product
  status VARCHAR(50) DEFAULT 'pending',
  progress JSONB DEFAULT '{}',
  config JSONB DEFAULT '{}',
  error TEXT,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```

---

## Schémas JSONB clés

### Product raw_data / current_data
```json
{
  "id": "SKU123",
  "title": "Nike Air Max 90",
  "description": "...",
  "image_link": "https://...",
  "price": "89.99 EUR",
  "brand": "Nike",
  "color": "White/Black",
  "gender": "male",
  "material": "Leather and textile"
}
```

### Agent trace tool_input/output
```json
// tool_input pour web_search
{
  "query": "Nike Air Max 90 specifications",
  "site": "nike.com"
}

// tool_output
{
  "results": [
    { "title": "...", "url": "...", "snippet": "..." }
  ]
}
```

### Proposal sources
```json
[
  {
    "type": "web",
    "reference": "https://nike.com/air-max-90",
    "evidence": "Leather and textile upper, Color: White/Black",
    "confidence": 0.95
  },
  {
    "type": "vision",
    "reference": "https://cdn.shop.com/image.jpg",
    "evidence": "Couleur blanche visible, accents noirs",
    "confidence": 0.92
  }
]
```

### Rule condition
```json
// max_length
{ "operator": "max_length", "value": 150 }

// required
{ "operator": "required" }

// pattern
{ "operator": "pattern", "value": "^[A-Z].*" }

// forbidden_words
{ "operator": "forbidden_words", "value": ["gratuit", "meilleur", "n°1"] }
```

### Job progress
```json
{
  "total_products": 1250,
  "processed": 847,
  "succeeded": 820,
  "failed": 27,
  "current_product_id": "uuid"
}
```
