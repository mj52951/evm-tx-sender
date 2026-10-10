package config

import (
	"fmt"
	"log/slog"

	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type Wallet struct {
	PrivateKey *ecdsa.PrivateKey
	Address    common.Address
}

func (w Wallet) String() string { return w.Address.Hex() }

// LogValue redacts the private key when the wallet is logged via slog.
func (w Wallet) LogValue() slog.Value { return slog.StringValue(w.Address.Hex()) }

// LoadWallets parses the hex private keys from env into addressable wallets.
func LoadWallets(cfg *EnvConfig) ([]Wallet, error) {
	wallets := make([]Wallet, 0, len(cfg.PrivateKeys))
	seen := make(map[common.Address]struct{}, len(cfg.PrivateKeys))
	for i, hex := range cfg.PrivateKeys {
		pk, err := crypto.ToECDSA(common.FromHex(hex))
		if err != nil {
			// Index only - never echo the key material.
			return nil, fmt.Errorf("PRIVATE_KEYS[%d]: invalid hex key", i)
		}
		address := crypto.PubkeyToAddress(pk.PublicKey)
		if _, dup := seen[address]; dup {
			slog.Warn("skipping duplicate wallet", "address", address.Hex(), "index", i)
			continue
		}
		seen[address] = struct{}{}
		wallets = append(wallets, Wallet{PrivateKey: pk, Address: address})
	}
	return wallets, nil
}
