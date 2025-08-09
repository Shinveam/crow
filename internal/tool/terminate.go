package tool

import (
	"context"
	"fmt"

	"crow/internal/schema"
)

type Terminate struct {
	name string
}

func NewTerminate() *Terminate {
	return &Terminate{name: "terminate"}
}

func (t *Terminate) GetName() string {
	return t.name
}

func (t *Terminate) GetTool() schema.Tool {
	return schema.Tool{
		Type: "function",
		Function: schema.ToolFunction{
			Name:        "terminate",
			Description: "当以下条件的其中一个或多个得到满足时，请调用此工具以用来终止交互:\n1. 用户的需求得到满足时; 2. 无法继续执行任务时; 3. 需要向用户询问以获得信息时。",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type":        "string",
						"description": "交互的完成状态，成功为success，失败为failure",
						"enum":        []string{"success", "failure"},
					},
				},
				"required": []string{"status"},
			},
		},
	}
}

func (t *Terminate) Execute(ctx context.Context, arguments map[string]any) (string, error) {
	if arguments == nil {
		return "", fmt.Errorf("missing arguments for tool call: %s", t.name)
	}
	status, ok := arguments["status"].(string)
	if !ok || (status != "success" && status != "failure") {
		return "", fmt.Errorf("invalid status value: %s", status)
	}
	return fmt.Sprintf("The interaction has been completed with status: %s", status), nil
}
