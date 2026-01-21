package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/benjamincozon/feedenrich/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// SessionContext is passed to tools for context
type SessionContext interface {
	GetProductData() json.RawMessage
	AddProposal(field, before, after string, sources []Source, confidence float64, risk string)
	AddSource(source Source)
}

// Source represents evidence for a fact
type Source struct {
	Type       string  `json:"type"`
	Reference  string  `json:"reference"`
	Evidence   string  `json:"evidence"`
	Confidence float64 `json:"confidence"`
}

// Toolbox holds all available tools
type Toolbox struct {
	config *config.Config
	tools  map[string]Tool
	client *openai.Client
}

// Tool is an executable tool
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, input json.RawMessage, session SessionContext) (any, error)
}

// New creates a new Toolbox
func New(cfg *config.Config) *Toolbox {
	client := openai.NewClient(cfg.OpenAI.APIKey)
	
	tb := &Toolbox{
		config: cfg,
		tools:  make(map[string]Tool),
		client: client,
	}

	// Register all tools
	tb.Register(&AnalyzeProductTool{client: client, config: cfg})
	tb.Register(&WebSearchTool{config: cfg})
	tb.Register(&FetchPageTool{})
	tb.Register(&AnalyzeImageTool{client: client, config: cfg})
	tb.Register(&OptimizeFieldTool{client: client, config: cfg})
	tb.Register(&AddAttributeTool{})
	tb.Register(&ValidateProposalTool{client: client, config: cfg})
	tb.Register(&CommitChangesTool{})
	tb.Register(&RequestHumanReviewTool{})

	return tb
}

// Register adds a tool to the toolbox
func (tb *Toolbox) Register(tool Tool) {
	tb.tools[tool.Name()] = tool
}

// Execute runs a tool by name
func (tb *Toolbox) Execute(ctx context.Context, name string, input json.RawMessage, session SessionContext) (any, error) {
	tool, ok := tb.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return tool.Execute(ctx, input, session)
}

// OpenAITools returns the tools in OpenAI format
func (tb *Toolbox) OpenAITools() []openai.Tool {
	var result []openai.Tool
	for _, tool := range tb.tools {
		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return result
}
