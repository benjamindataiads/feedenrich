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

	// Products
	api.GET("/datasets/:id/products", h.ListProducts)
	api.GET("/products/:id", h.GetProduct)

	// Agent
	api.POST("/products/:id/enrich", h.EnrichProduct)
	api.POST("/datasets/:id/enrich", h.EnrichDataset)
	api.GET("/agent/sessions/:id", h.GetAgentSession)
	api.GET("/agent/sessions/:id/trace", h.GetAgentTrace)

	// Proposals
	api.GET("/proposals", h.ListProposals)
	api.GET("/proposals/with-products", h.ListProposalsWithProducts)
	api.GET("/proposals/:id", h.GetProposal)
	api.PATCH("/proposals/:id", h.UpdateProposal)
	api.POST("/proposals/bulk", h.BulkUpdateProposals)

	// Rules
	api.GET("/rules", h.ListRules)
	api.POST("/rules", h.CreateRule)
	api.PATCH("/rules/:id", h.UpdateRule)
	api.DELETE("/rules/:id", h.DeleteRule)

	// Prompts
	api.GET("/prompts", h.ListPrompts)
	api.GET("/prompts/:id", h.GetPrompt)
	api.PATCH("/prompts/:id", h.UpdatePrompt)

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
