package config

import (
	"errors"
	"fmt"
	"strconv"
)

type ChainConfig struct {
	ChainID uint64
	RPCURL  string
}

// LoadChains resolves the per-chain config from parsed env values.
func LoadChains(cfg *EnvConfig) (map[uint64]ChainConfig, error) {
	chains := make(map[uint64]ChainConfig, len(cfg.RPCURLs))
	for key, url := range cfg.RPCURLs {
		chainID, err := strconv.ParseUint(key, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CHAIN_RPC_URLS: invalid chain id %q", key)
		}
		if chainID == 0 {
			return nil, errors.New("CHAIN_RPC_URLS: chain id must be > 0")
		}
		if url == "" {
			return nil, fmt.Errorf("CHAIN_RPC_URLS: empty url for chain %d", chainID)
		}
		chains[chainID] = ChainConfig{ChainID: chainID, RPCURL: url}
	}
	return chains, nil
}
