package api

import (
	"context"
	"net/http"

	"github.com/benjamincozon/feedenrich/internal/agent"
	"github.com/benjamincozon/feedenrich/internal/agent/tools"
	"github.com/benjamincozon/feedenrich/internal/api/handlers"
	"github.com/benjamincozon/feedenrich/internal/config"
	"github.com/benjamincozon/feedenrich/internal/db"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Server struct {
	echo    *echo.Echo
	config  *config.Config
	queries *db.Queries
	agent   *agent.Agent
}

func NewServer(cfg *config.Config, queries *db.Queries) *Server {
	e := echo.New()
	e.HideBanner = true

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Create toolbox and agent
	toolbox := tools.New(cfg)
	agnt := agent.New(cfg, toolbox)
	
	// Set token tracker to record usage to database
	agnt.SetTokenTracker(queries)

	s := &Server{
		echo:    e,
		config:  cfg,
		queries: queries,
		agent:   agnt,
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// Health check
	s.echo.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// API routes
	api := s.echo.Group("/api")

	// Datasets
	h := handlers.NewHandlers(s.config, s.queries, s.agent)
	api.POST("/datasets/upload", h.UploadDataset)
	api.GET("/datasets", h.ListDatasets)
	api.GET("/datasets/:id", h.GetDataset)
	api.DELETE("/datasets/:id", h.DeleteDataset)
	api.GET("/datasets/:id/export", h.ExportDataset)
	api.GET("/datasets/:id/stats", h.GetDatasetStats)

	// Data Feeds - Versions, Snapshots, Change Log
	api.GET("/datasets/:id/versions", h.ListDatasetVersions)
	api.POST("/datasets/:id/snapshots", h.CreateSnapshot)
	api.GET("/datasets/:id/snapshots", h.ListSnapshots)
	api.DELETE("/snapshots/:id", h.DeleteSnapshot)
	api.GET("/datasets/:id/changelog", h.GetChangeLog)

	// Products
	api.GET("/datasets/:id/products", h.ListProducts)
	api.GET("/products/:id", h.GetProduct)

	// Agent
	api.POST("/products/:id/enrich", h.EnrichProduct)
	api.POST("/datasets/:id/enrich", h.EnrichDataset)
	api.GET("/agent/sessions/:id", h.GetAgentSession)
	api.GET("/agent/sessions/:id/trace", h.GetAgentTrace)

	// Feed Audit
	api.GET("/audit/groups", h.GetAuditGroups)
	api.POST("/datasets/:id/audit", h.AuditDataset)

	// Jobs (Execution tracking)
	api.GET("/jobs", h.ListJobs)
	api.GET("/jobs/:id", h.GetJobDetails)

	// Proposals
	api.GET("/proposals", h.ListProposals)
	api.GET("/proposals/with-products", h.ListProposalsWithProducts)
	api.GET("/proposals/by-module", h.GetProposalsByModule)
	api.GET("/proposals/module", h.ListProposalsByModuleFiltered)
	api.GET("/proposals/:id", h.GetProposal)
	api.PATCH("/proposals/:id", h.UpdateProposal)
	api.POST("/proposals/bulk", h.BulkUpdateProposals)
	api.POST("/proposals/apply-rules", h.ApplyApprovalRules)

	// Approval Rules
	api.GET("/approval-rules", h.ListApprovalRules)
	api.POST("/approval-rules", h.CreateApprovalRule)
	api.PATCH("/approval-rules/:id", h.UpdateApprovalRule)
	api.DELETE("/approval-rules/:id", h.DeleteApprovalRule)

	// Rules (validation rules - legacy)
	api.GET("/rules", h.ListRules)
	api.POST("/rules", h.CreateRule)
	api.PATCH("/rules/:id", h.UpdateRule)
	api.DELETE("/rules/:id", h.DeleteRule)

	// Prompts
	api.GET("/prompts", h.ListPrompts)
	api.GET("/prompts/:id", h.GetPrompt)
	api.PATCH("/prompts/:id", h.UpdatePrompt)

	// Token usage stats
	api.GET("/token-usage", h.GetTokenUsageStats)

	// Serve static files for frontend
	s.echo.Static("/", "web/static")
}

func (s *Server) Start(ctx context.Context) error {
	addr := ":" + s.config.Server.Port
	return s.echo.Start(addr)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.echo.Shutdown(ctx)
}
