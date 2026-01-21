package db

import (
	"context"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/agent"
	"github.com/benjamincozon/feedenrich/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Queries wraps database operations
type Queries struct {
	pool *pgxpool.Pool
}

// New creates a new Queries instance
func New(pool *pgxpool.Pool) *Queries {
	return &Queries{pool: pool}
}

// Connect establishes a database connection pool
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

// Dataset operations

func (q *Queries) CreateDataset(ctx context.Context, d models.Dataset) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO datasets (id, name, source_file_url, row_count, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, d.ID, d.Name, d.SourceFileURL, d.RowCount, d.Status, d.CreatedAt, d.UpdatedAt)
	return err
}

func (q *Queries) GetDataset(ctx context.Context, id uuid.UUID) (*models.Dataset, error) {
	var d models.Dataset
	err := q.pool.QueryRow(ctx, `
		SELECT id, name, source_file_url, row_count, status, created_at, updated_at
		FROM datasets WHERE id = $1
	`, id).Scan(&d.ID, &d.Name, &d.SourceFileURL, &d.RowCount, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (q *Queries) ListDatasets(ctx context.Context) ([]models.Dataset, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, name, source_file_url, row_count, status, created_at, updated_at
		FROM datasets ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var datasets []models.Dataset
	for rows.Next() {
		var d models.Dataset
		if err := rows.Scan(&d.ID, &d.Name, &d.SourceFileURL, &d.RowCount, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		datasets = append(datasets, d)
	}
	return datasets, nil
}

func (q *Queries) DeleteDataset(ctx context.Context, id uuid.UUID) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM datasets WHERE id = $1`, id)
	return err
}

func (q *Queries) GetDatasetStats(ctx context.Context, id uuid.UUID) (map[string]any, error) {
	var total, enriched, pending int
	err := q.pool.QueryRow(ctx, `
		SELECT 
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'enriched'),
			COUNT(*) FILTER (WHERE status = 'pending')
		FROM products WHERE dataset_id = $1
	`, id).Scan(&total, &enriched, &pending)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"products": map[string]int{
			"total":    total,
			"enriched": enriched,
			"pending":  pending,
		},
	}, nil
}

// Product operations

func (q *Queries) CreateProduct(ctx context.Context, p models.Product) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO products (id, dataset_id, external_id, raw_data, current_data, version, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, p.ID, p.DatasetID, p.ExternalID, p.RawData, p.CurrentData, p.Version, p.Status, p.CreatedAt, p.UpdatedAt)
	return err
}

func (q *Queries) GetProduct(ctx context.Context, id uuid.UUID) (*models.Product, error) {
	var p models.Product
	err := q.pool.QueryRow(ctx, `
		SELECT id, dataset_id, external_id, raw_data, current_data, version, status, agent_readiness_score, created_at, updated_at
		FROM products WHERE id = $1
	`, id).Scan(&p.ID, &p.DatasetID, &p.ExternalID, &p.RawData, &p.CurrentData, &p.Version, &p.Status, &p.AgentReadinessScore, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (q *Queries) ListProductsByDataset(ctx context.Context, datasetID uuid.UUID) ([]models.Product, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, dataset_id, external_id, raw_data, current_data, version, status, agent_readiness_score, created_at, updated_at
		FROM products WHERE dataset_id = $1 ORDER BY created_at
	`, datasetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.DatasetID, &p.ExternalID, &p.RawData, &p.CurrentData, &p.Version, &p.Status, &p.AgentReadinessScore, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, nil
}

// Agent session operations

func (q *Queries) CreateAgentSession(ctx context.Context, s agent.Session) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO agent_sessions (id, product_id, goal, status, total_steps, tokens_used, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, s.ID, s.ProductID, s.Goal, s.Status, len(s.Traces), 0, s.StartedAt, nil)
	if err != nil {
		return err
	}

	// Save traces
	for _, t := range s.Traces {
		_, err := q.pool.Exec(ctx, `
			INSERT INTO agent_traces (id, session_id, step_number, thought, tool_name, tool_input, tool_output, tokens_used, duration_ms, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, t.ID, t.SessionID, t.StepNumber, t.Thought, t.ToolName, t.ToolInput, t.ToolOutput, t.TokensUsed, t.DurationMs, t.CreatedAt)
		if err != nil {
			return err
		}
	}

	// Save proposals
	for _, p := range s.Proposals {
		_, err := q.pool.Exec(ctx, `
			INSERT INTO proposals (id, product_id, session_id, field, before_value, after_value, sources, confidence, risk_level, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, p.ID, p.ProductID, p.SessionID, p.Field, p.BeforeValue, p.AfterValue, p.Sources, p.Confidence, p.RiskLevel, p.Status, p.CreatedAt)
		if err != nil {
			return err
		}
	}

	return nil
}

func (q *Queries) GetAgentSession(ctx context.Context, id uuid.UUID) (*models.AgentSession, error) {
	var s models.AgentSession
	err := q.pool.QueryRow(ctx, `
		SELECT id, product_id, goal, status, total_steps, tokens_used, started_at, completed_at
		FROM agent_sessions WHERE id = $1
	`, id).Scan(&s.ID, &s.ProductID, &s.Goal, &s.Status, &s.TotalSteps, &s.TokensUsed, &s.StartedAt, &s.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (q *Queries) GetAgentTraces(ctx context.Context, sessionID uuid.UUID) ([]models.AgentTrace, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, session_id, step_number, thought, tool_name, tool_input, tool_output, tokens_used, duration_ms, created_at
		FROM agent_traces WHERE session_id = $1 ORDER BY step_number
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traces []models.AgentTrace
	for rows.Next() {
		var t models.AgentTrace
		if err := rows.Scan(&t.ID, &t.SessionID, &t.StepNumber, &t.Thought, &t.ToolName, &t.ToolInput, &t.ToolOutput, &t.TokensUsed, &t.DurationMs, &t.CreatedAt); err != nil {
			return nil, err
		}
		traces = append(traces, t)
	}
	return traces, nil
}

// Proposal operations

func (q *Queries) ListProposals(ctx context.Context) ([]models.Proposal, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, product_id, session_id, field, before_value, after_value, sources, confidence, risk_level, status, reviewed_by, reviewed_at, created_at
		FROM proposals ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var proposals []models.Proposal
	for rows.Next() {
		var p models.Proposal
		if err := rows.Scan(&p.ID, &p.ProductID, &p.SessionID, &p.Field, &p.BeforeValue, &p.AfterValue, &p.Sources, &p.Confidence, &p.RiskLevel, &p.Status, &p.ReviewedBy, &p.ReviewedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		proposals = append(proposals, p)
	}
	return proposals, nil
}

func (q *Queries) GetProposal(ctx context.Context, id uuid.UUID) (*models.Proposal, error) {
	var p models.Proposal
	err := q.pool.QueryRow(ctx, `
		SELECT id, product_id, session_id, field, before_value, after_value, sources, confidence, risk_level, status, reviewed_by, reviewed_at, created_at
		FROM proposals WHERE id = $1
	`, id).Scan(&p.ID, &p.ProductID, &p.SessionID, &p.Field, &p.BeforeValue, &p.AfterValue, &p.Sources, &p.Confidence, &p.RiskLevel, &p.Status, &p.ReviewedBy, &p.ReviewedAt, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (q *Queries) UpdateProposalStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := q.pool.Exec(ctx, `UPDATE proposals SET status = $2, reviewed_at = NOW() WHERE id = $1`, id, status)
	return err
}

// Job operations

func (q *Queries) CreateJob(ctx context.Context, j models.Job) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO jobs (id, dataset_id, type, status, progress, config, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, j.ID, j.DatasetID, j.Type, j.Status, j.Progress, j.Config, j.CreatedAt)
	return err
}

// Rule operations

func (q *Queries) ListRules(ctx context.Context) ([]models.Rule, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT id, dataset_id, name, type, field, condition, message, severity, active, created_by, created_at
		FROM rules WHERE active = true ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []models.Rule
	for rows.Next() {
		var r models.Rule
		if err := rows.Scan(&r.ID, &r.DatasetID, &r.Name, &r.Type, &r.Field, &r.Condition, &r.Message, &r.Severity, &r.Active, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func (q *Queries) CreateRule(ctx context.Context, r models.Rule) error {
	_, err := q.pool.Exec(ctx, `
		INSERT INTO rules (id, dataset_id, name, type, field, condition, message, severity, active, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, r.ID, r.DatasetID, r.Name, r.Type, r.Field, r.Condition, r.Message, r.Severity, r.Active, r.CreatedBy, r.CreatedAt)
	return err
}

func (q *Queries) UpdateRule(ctx context.Context, r models.Rule) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE rules SET name = $2, type = $3, field = $4, condition = $5, message = $6, severity = $7, active = $8
		WHERE id = $1
	`, r.ID, r.Name, r.Type, r.Field, r.Condition, r.Message, r.Severity, r.Active)
	return err
}

func (q *Queries) DeleteRule(ctx context.Context, id uuid.UUID) error {
	_, err := q.pool.Exec(ctx, `DELETE FROM rules WHERE id = $1`, id)
	return err
}
