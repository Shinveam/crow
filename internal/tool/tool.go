package tool

import (
	"context"

	"crow/internal/schema"
)

type Caller interface {
	// GetName 获取工具名称
	GetName() string
	// GetTool 获取工具
	GetTool() schema.Tool
	// Execute 执行工具
	// @param arguments: 需要执行的参数
	// @return string: 执行的结果
	Execute(ctx context.Context, arguments map[string]any) (string, error)
}
