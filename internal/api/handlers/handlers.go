package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/benjamincozon/feedenrich/internal/agent"
	"github.com/benjamincozon/feedenrich/internal/config"
	"github.com/benjamincozon/feedenrich/internal/db"
	"github.com/benjamincozon/feedenrich/internal/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type Handlers struct {
	config  *config.Config
	queries *db.Queries
	agent   *agent.Agent
}

func NewHandlers(cfg *config.Config, queries *db.Queries, agnt *agent.Agent) *Handlers {
	return &Handlers{
		config:  cfg,
		queries: queries,
		agent:   agnt,
	}
}

// UploadDataset handles TSV/CSV file upload
func (h *Handlers) UploadDataset(c echo.Context) error {
	name := c.FormValue("name")
	if name == "" {
		name = "Untitled Dataset"
	}

	file, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "No file uploaded")
	}

	src, err := file.Open()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to open file")
	}
	defer src.Close()

	// Save file locally (in production, use S3/GCS)
	uploadDir := h.config.Storage.Path
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create upload dir")
	}

	datasetID := uuid.New()
	filename := fmt.Sprintf("%s_%s", datasetID.String(), file.Filename)
	filePath := filepath.Join(uploadDir, filename)

	dst, err := os.Create(filePath)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save file")
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to copy file")
	}

	// Parse the file to get row count and detect schema
	rowCount, products, err := h.parseFile(filePath)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, fmt.Sprintf("Failed to parse file: %v", err))
	}

	// Create dataset in DB
	dataset := models.Dataset{
		ID:            datasetID,
		Name:          name,
		SourceFileURL: filePath,
		RowCount:      rowCount,
		Status:        "uploaded",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := h.queries.CreateDataset(c.Request().Context(), dataset); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create dataset")
	}

	// Create products
	for _, p := range products {
		if err := h.queries.CreateProduct(c.Request().Context(), p); err != nil {
			// Log error but continue
			fmt.Printf("Failed to create product: %v\n", err)
		}
	}

	return c.JSON(http.StatusCreated, dataset)
}

func (h *Handlers) parseFile(filePath string) (int, []models.Product, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, nil, err
	}
	defer file.Close()

	// Detect delimiter (tab or comma)
	buf := make([]byte, 1024)
	n, _ := file.Read(buf)
	file.Seek(0, 0)

	delimiter := '\t'
	if strings.Count(string(buf[:n]), ",") > strings.Count(string(buf[:n]), "\t") {
		delimiter = ','
	}

	reader := csv.NewReader(file)
	reader.Comma = delimiter
	reader.LazyQuotes = true

	// Read header
	header, err := reader.Read()
	if err != nil {
		return 0, nil, fmt.Errorf("read header: %w", err)
	}

	// Normalize header names
	headerMap := make(map[string]int)
	for i, h := range header {
		headerMap[strings.ToLower(strings.TrimSpace(h))] = i
	}

	var products []models.Product
	rowCount := 0

	datasetID := uuid.MustParse(filepath.Base(filePath)[:36])

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // Skip malformed rows
		}

		rowCount++

		// Build product data
		data := make(map[string]string)
		for name, idx := range headerMap {
			if idx < len(record) {
				data[name] = record[idx]
			}
		}

		rawData, _ := json.Marshal(data)

		// Get external ID
		externalID := ""
		if idx, ok := headerMap["id"]; ok && idx < len(record) {
			externalID = record[idx]
		} else if idx, ok := headerMap["offer_id"]; ok && idx < len(record) {
			externalID = record[idx]
		} else {
			externalID = fmt.Sprintf("row_%d", rowCount)
		}

		products = append(products, models.Product{
			ID:          uuid.New(),
			DatasetID:   datasetID,
			ExternalID:  externalID,
			RawData:     rawData,
			CurrentData: rawData,
			Version:     1,
			Status:      "pending",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		})
	}

	return rowCount, products, nil
}

// ListDatasets returns all datasets
func (h *Handlers) ListDatasets(c echo.Context) error {
	datasets, err := h.queries.ListDatasets(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to list datasets")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": datasets})
}

// GetDataset returns a single dataset
func (h *Handlers) GetDataset(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid dataset ID")
	}

	dataset, err := h.queries.GetDataset(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Dataset not found")
	}

	return c.JSON(http.StatusOK, dataset)
}

// DeleteDataset deletes a dataset
func (h *Handlers) DeleteDataset(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid dataset ID")
	}

	if err := h.queries.DeleteDataset(c.Request().Context(), id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete dataset")
	}

	return c.NoContent(http.StatusNoContent)
}

// ExportDataset exports the enriched dataset
func (h *Handlers) ExportDataset(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid dataset ID")
	}

	format := c.QueryParam("format")
	if format == "" {
		format = "tsv"
	}

	products, err := h.queries.ListProductsByDataset(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get products")
	}

	if format == "json" {
		return c.JSON(http.StatusOK, products)
	}

	// TSV/CSV export
	c.Response().Header().Set("Content-Type", "text/tab-separated-values")
	c.Response().Header().Set("Content-Disposition", "attachment; filename=export.tsv")

	// TODO: proper TSV generation
	return c.String(http.StatusOK, "Export functionality")
}

// GetDatasetStats returns statistics for a dataset
func (h *Handlers) GetDatasetStats(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid dataset ID")
	}

	stats, err := h.queries.GetDatasetStats(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get stats")
	}

	return c.JSON(http.StatusOK, stats)
}

// ListProducts returns products for a dataset
func (h *Handlers) ListProducts(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid dataset ID")
	}

	products, err := h.queries.ListProductsByDataset(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to list products")
	}

	return c.JSON(http.StatusOK, map[string]any{"data": products})
}

// GetProduct returns a single product
func (h *Handlers) GetProduct(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid product ID")
	}

	product, err := h.queries.GetProduct(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Product not found")
	}

	return c.JSON(http.StatusOK, product)
}

// EnrichProduct starts agent enrichment on a single product
func (h *Handlers) EnrichProduct(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid product ID")
	}

	product, err := h.queries.GetProduct(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Product not found")
	}

	var req struct {
		Goal   string         `json:"goal"`
		Config map[string]any `json:"config"`
	}
	if err := c.Bind(&req); err != nil {
		req.Goal = "GMC compliance + agent readiness"
	}

	// Run agent in background with separate context
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		
		fmt.Printf("Starting agent for product %s with goal: %s\n", product.ID, req.Goal)
		
		session, err := h.agent.Run(ctx, product, req.Goal)
		if err != nil {
			fmt.Printf("Agent error for product %s: %v\n", product.ID, err)
			return
		}

		fmt.Printf("Agent completed for product %s: %d steps, %d proposals\n", product.ID, len(session.Traces), len(session.Proposals))

		// Save session and proposals to DB
		if err := h.queries.CreateAgentSession(ctx, *session); err != nil {
			fmt.Printf("Failed to save session for product %s: %v\n", product.ID, err)
		}
	}()

	return c.JSON(http.StatusAccepted, map[string]string{
		"status":  "started",
		"message": "Agent enrichment started",
	})
}

// EnrichDataset starts batch enrichment for all products
func (h *Handlers) EnrichDataset(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid dataset ID")
	}

	// Create a job (in production, this would be queued)
	job := models.Job{
		ID:        uuid.New(),
		DatasetID: id,
		Type:      "enrich_all",
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	if err := h.queries.CreateJob(c.Request().Context(), job); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create job")
	}

	return c.JSON(http.StatusAccepted, job)
}

// GetAgentSession returns an agent session
func (h *Handlers) GetAgentSession(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid session ID")
	}

	session, err := h.queries.GetAgentSession(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Session not found")
	}

	return c.JSON(http.StatusOK, session)
}

// GetAgentTrace returns the full trace for a session
func (h *Handlers) GetAgentTrace(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid session ID")
	}

	traces, err := h.queries.GetAgentTraces(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get traces")
	}

	return c.JSON(http.StatusOK, map[string]any{"steps": traces})
}

// ListProposals returns proposals with filters
func (h *Handlers) ListProposals(c echo.Context) error {
	proposals, err := h.queries.ListProposals(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to list proposals")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": proposals})
}

// GetProposal returns a single proposal
func (h *Handlers) GetProposal(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid proposal ID")
	}

	proposal, err := h.queries.GetProposal(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Proposal not found")
	}

	return c.JSON(http.StatusOK, proposal)
}

// UpdateProposal updates a proposal (accept/reject/edit)
func (h *Handlers) UpdateProposal(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid proposal ID")
	}

	var req struct {
		Action      string `json:"action"` // accept, reject, edit
		EditedValue string `json:"edited_value,omitempty"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	status := "proposed"
	switch req.Action {
	case "accept":
		status = "accepted"
	case "reject":
		status = "rejected"
	case "edit":
		status = "edited"
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid action")
	}

	if err := h.queries.UpdateProposalStatus(c.Request().Context(), id, status); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update proposal")
	}

	return c.JSON(http.StatusOK, map[string]string{"status": status})
}

// BulkUpdateProposals updates multiple proposals
func (h *Handlers) BulkUpdateProposals(c echo.Context) error {
	var req struct {
		Action  string `json:"action"`
		Filters struct {
			DatasetID string `json:"dataset_id"`
			RiskLevel string `json:"risk_level"`
			Status    string `json:"status"`
		} `json:"filters"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	// TODO: implement bulk update
	return c.JSON(http.StatusOK, map[string]int{"updated": 0})
}

// ListRules returns all rules
func (h *Handlers) ListRules(c echo.Context) error {
	rules, err := h.queries.ListRules(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to list rules")
	}
	return c.JSON(http.StatusOK, map[string]any{"data": rules})
}

// CreateRule creates a new rule
func (h *Handlers) CreateRule(c echo.Context) error {
	var rule models.Rule
	if err := c.Bind(&rule); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	rule.ID = uuid.New()
	rule.CreatedAt = time.Now()

	if err := h.queries.CreateRule(c.Request().Context(), rule); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create rule")
	}

	return c.JSON(http.StatusCreated, rule)
}

// UpdateRule updates a rule
func (h *Handlers) UpdateRule(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid rule ID")
	}

	var rule models.Rule
	if err := c.Bind(&rule); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}
	rule.ID = id

	if err := h.queries.UpdateRule(c.Request().Context(), rule); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to update rule")
	}

	return c.JSON(http.StatusOK, rule)
}

// DeleteRule deletes a rule
func (h *Handlers) DeleteRule(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid rule ID")
	}

	if err := h.queries.DeleteRule(c.Request().Context(), id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete rule")
	}

	return c.NoContent(http.StatusNoContent)
}
