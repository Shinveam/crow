package tool

import (
	"context"
	"time"

	"crow/internal/schema"
)

type CurrentTime struct {
	name string
}

func NewCurrentTime() *CurrentTime {
	return &CurrentTime{name: "current_time"}
}

func (c *CurrentTime) GetName() string {
	return c.name
}

func (c *CurrentTime) GetTool() schema.Tool {
	return schema.Tool{
		Type: "function",
		Function: schema.ToolFunction{
			Name:        "current_time",
			Description: "获取当前的日期和时间，格式为YYYY-MM-DD HH:MM:SS，支持指定时区。当询问几点时，不需要回答日期，只回答时间。同理，当询问日期时，不需要回答时间，只回答日期。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timezone": map[string]any{
						"type":        "string",
						"description": "时区标识符，如Asia/Shanghai",
						"default":     "Local",
					},
				},
			},
		},
	}
}

func (c *CurrentTime) Execute(ctx context.Context, arguments map[string]any) (string, error) {
	local := time.Local // 默认使用本地时区
	if arguments != nil {
		timezone, ok := arguments["timezone"].(string)
		if ok && timezone != "" {
			if loc, err := time.LoadLocation(timezone); err == nil {
				local = loc
			}
		}
	}

	return time.Now().In(local).Format("2006-01-02 15:04:05"), nil
}
