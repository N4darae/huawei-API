//go:build !dev

package main

import (
	"context"
	"errors"

	"github.com/n4darae/huawei-API/src/internal/auth"
	"github.com/n4darae/huawei-API/src/internal/config"
)

var ErrDevSeedNotBuilt = errors.New("--dev-seed needs a binary built with -tags dev; this one refuses to create a known account")

func seedDevAdmin(_ context.Context, cfg config.Config, _ *auth.Sessions) error {
	if !cfg.DevSeed {
		return nil
	}
	return ErrDevSeedNotBuilt
}
