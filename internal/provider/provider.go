package provider

import (
	"context"

	"aria2-transfer-gateway/internal/domain"
)

type TransferRequest struct {
	SourceDir   string
	TargetPath  string
	Files       []string
	Destination domain.Destination
}

type Provider interface {
	Transfer(ctx context.Context, request TransferRequest) error
}
