package provider

import (
	"context"

	"aria2-transfer-gateway/internal/domain"
)

type TransferProgress struct {
	TotalBytes       int64
	TransferredBytes int64
}

type TransferRequest struct {
	SourceDir   string
	TargetPath  string
	Files       []string
	Destination domain.Destination
	OnProgress  func(TransferProgress)
}

type Provider interface {
	Transfer(ctx context.Context, request TransferRequest) error
}
