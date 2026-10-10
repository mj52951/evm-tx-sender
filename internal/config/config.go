package config

import (
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
)

type LogConfig struct {
	JSON  bool   `env:"LOG_JSON"  envDefault:"false"`
	Level string `env:"LOG_LEVEL" envDefault:"info" validate:"oneof=debug info warn error"`
}

// SenderConfig holds the replace-by-fee and backpressure knobs shared by every worker.
type SenderConfig struct {
	ResendInterval time.Duration `env:"RESEND_INTERVAL" envDefault:"30s"   validate:"gte=1s"`
	MaxBumps       int           `env:"MAX_BUMPS"       envDefault:"3"     validate:"gte=0"`
	BumpPercent    uint          `env:"BUMP_PERCENT"    envDefault:"125"   validate:"gte=110"`
	IntakeBuffer   int           `env:"INTAKE_BUFFER"   envDefault:"1024"  validate:"gte=1"`
	TickInterval   time.Duration `env:"TICK_INTERVAL"   envDefault:"1s"    validate:"gte=100ms"`
}

type EnvConfig struct {
	HTTPAddr string `env:"HTTP_ADDR" envDefault:"0.0.0.0:8080" validate:"hostname_port"`

	Log    LogConfig
	Sender SenderConfig

	// Format: CHAIN_RPC_URLS=1=https://...,10=https://...
	RPCURLs map[string]string `env:"CHAIN_RPC_URLS,required" envKeyValSeparator:"=" validate:"required,min=1"`

	// Env var is cleared after reading so crash dumps or child processes cannot see it.
	PrivateKeys []string `env:"PRIVATE_KEYS,required,unset" validate:"required,min=1"`
}

func LoadEnvConfig() (*EnvConfig, error) {
	_ = godotenv.Load()

	cfg, err := env.ParseAs[EnvConfig]()
	if err != nil {
		return nil, err
	}
	if err := validator.New().Struct(cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
