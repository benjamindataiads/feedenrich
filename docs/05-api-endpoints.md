# API Endpoints

## Datasets

```
POST   /api/datasets/upload          Upload TSV file
GET    /api/datasets                 List datasets
GET    /api/datasets/:id             Get dataset details + stats
DELETE /api/datasets/:id             Delete dataset
GET    /api/datasets/:id/export      Export enriched dataset
```

## Products

```
GET    /api/datasets/:id/products    List products (paginated, filterable)
GET    /api/products/:id             Get product with current state
GET    /api/products/:id/history     Get product version history
```

## Agent

```
POST   /api/products/:id/enrich      Start agent session on single product
POST   /api/datasets/:id/enrich      Start batch enrichment job
GET    /api/agent/sessions/:id       Get agent session status + trace
GET    /api/agent/sessions/:id/trace Get full reasoning trace
POST   /api/agent/sessions/:id/pause Pause running session
POST   /api/agent/sessions/:id/resume Resume paused session
```

### POST /api/products/:id/enrich
```json
// Request
{
  "goal": "GMC compliance + agent readiness",
  "config": {
    "max_steps": 20,
    "enable_web_search": true,
    "enable_vision": true,
    "auto_commit_low_risk": false
  }
}

// Response 202
{
  "session_id": "uuid",
  "status": "running"
}
```

### GET /api/agent/sessions/:id/trace
```json
// Response
{
  "session_id": "uuid",
  "product_id": "uuid",
  "goal": "GMC compliance + agent readiness",
  "status": "completed",
  "steps": [
    {
      "step": 1,
      "thought": "Je dois d'abord analyser l'état du produit",
      "tool": "analyze_product",
      "input": { "product_id": "..." },
      "output": { "quality_scores": { ... } },
      "duration_ms": 450
    },
    {
      "step": 2,
      "thought": "Le titre est trop court, je cherche le nom complet",
      "tool": "web_search",
      "input": { "query": "Nike SKU789", "site": "nike.com" },
      "output": { "results": [...] },
      "duration_ms": 820
    }
  ],
  "summary": {
    "total_steps": 9,
    "tokens_used": 4200,
    "duration_ms": 8500,
    "proposals_created": 5,
    "score_before": 0.28,
    "score_after": 0.86
  }
}
```

## Proposals

```
GET    /api/proposals                List proposals (filterable by status, risk)
GET    /api/proposals/:id            Get proposal details with sources
PATCH  /api/proposals/:id            Accept/reject/edit proposal
POST   /api/proposals/bulk           Bulk action on proposals
```

### PATCH /api/proposals/:id
```json
// Request
{
  "action": "accept" | "reject" | "edit",
  "edited_value": "..." // if action=edit
}

// Response
{
  "id": "uuid",
  "status": "accepted",
  "applied_at": "2024-01-15T11:30:00Z"
}
```

### POST /api/proposals/bulk
```json
// Request
{
  "action": "accept",
  "filters": {
    "dataset_id": "uuid",
    "risk_level": "low",
    "status": "proposed"
  }
}

// Response
{ "updated": 234 }
```

## Rules

```
GET    /api/rules                    List rules
POST   /api/rules                    Create rule
PATCH  /api/rules/:id                Update rule
DELETE /api/rules/:id                Delete rule
```

### POST /api/rules
```json
// Request
{
  "name": "Title minimum length",
  "type": "hard",
  "field": "title",
  "condition": { "operator": "min_length", "value": 30 },
  "message": "Title must be at least 30 characters",
  "severity": "error"
}
```

## Stats

```
GET    /api/datasets/:id/stats       Dataset statistics
```

### Response
```json
{
  "products": {
    "total": 1250,
    "enriched": 1180,
    "pending": 70
  },
  "scores": {
    "avg_agent_readiness_before": 0.42,
    "avg_agent_readiness_after": 0.81,
    "improvement": "+93%"
  },
  "proposals": {
    "total": 3200,
    "accepted": 2890,
    "rejected": 180,
    "pending_review": 130
  },
  "agent": {
    "total_sessions": 1250,
    "avg_steps_per_product": 7.2,
    "avg_duration_per_product": "6.4s",
    "total_tokens": 5250000
  }
}
```

## Streaming (WebSocket)

```
WS /api/agent/sessions/:id/stream    Stream agent reasoning in real-time
```

```json
// Messages streamed
{ "type": "thought", "content": "Je vais chercher le nom complet..." }
{ "type": "tool_call", "tool": "web_search", "input": {...} }
{ "type": "tool_result", "output": {...} }
{ "type": "proposal", "field": "title", "before": "...", "after": "..." }
{ "type": "completed", "summary": {...} }
```
