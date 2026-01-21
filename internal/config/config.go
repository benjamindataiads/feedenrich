package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Server struct {
		Port         string        `default:"8080" envconfig:"PORT"`
		ReadTimeout  time.Duration `default:"30s" envconfig:"READ_TIMEOUT"`
		WriteTimeout time.Duration `default:"30s" envconfig:"WRITE_TIMEOUT"`
	}

	Database struct {
		URL             string `required:"true" envconfig:"DATABASE_URL"`
		MaxConns        int    `default:"10" envconfig:"DB_MAX_CONNS"`
		MaxConnIdleTime string `default:"30m" envconfig:"DB_MAX_CONN_IDLE_TIME"`
	}

	OpenAI struct {
		APIKey string `required:"true" envconfig:"OPENAI_API_KEY"`
		Model  string `default:"gpt-4o" envconfig:"OPENAI_MODEL"`
	}

	Storage struct {
		Type   string `default:"local" envconfig:"STORAGE_TYPE"` // local, s3, gcs
		Path   string `default:"./uploads" envconfig:"STORAGE_PATH"`
		Bucket string `envconfig:"STORAGE_BUCKET"`
	}

	Agent struct {
		MaxSteps          int           `default:"20" envconfig:"AGENT_MAX_STEPS"`
		Timeout           time.Duration `default:"5m" envconfig:"AGENT_TIMEOUT"`
		EnableWebSearch   bool          `default:"true" envconfig:"AGENT_ENABLE_WEB_SEARCH"`
		EnableVision      bool          `default:"true" envconfig:"AGENT_ENABLE_VISION"`
		AutoCommitLowRisk bool          `default:"false" envconfig:"AGENT_AUTO_COMMIT_LOW_RISK"`
	}

	WebSearch struct {
		Provider string `default:"serper" envconfig:"WEBSEARCH_PROVIDER"` // serper, serpapi
		APIKey   string `envconfig:"SERPER_API_KEY"`
	}
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}
	return &cfg, nil
}
