//go:build tools

package tools

import (
	_ "github.com/bufbuild/buf/cmd/buf"
	_ "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	_ "github.com/google/wire/cmd/wire"
	_ "github.com/vektra/mockery/v3"
	_ "mvdan.cc/gofumpt"
)
