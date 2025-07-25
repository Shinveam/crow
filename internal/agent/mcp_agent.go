package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"crow/internal/config"
	"crow/internal/schema"
	"crow/internal/tool"
)

type MCPAgent struct {
	mcpConfig        *config.McpConfig
	mcpClient        *tool.MCPClient
	tools            map[string]tool.Caller
	specialToolNames []string
}

func NewMCPAgent(ctx context.Context, headers map[string]string) (*MCPAgent, error) {
	terminateTool := tool.NewTerminate()
	curTimeTool := tool.NewCurrentTime()
	agent := &MCPAgent{
		tools: map[string]tool.Caller{
			terminateTool.GetName(): terminateTool,
			curTimeTool.GetName():   curTimeTool,
		},
		specialToolNames: []string{terminateTool.GetName()},
	}
	err := agent.initializeMCPClient(ctx, "mcp", "1.0.0", headers)
	if err != nil {
		return nil, err
	}
	return agent, nil
}

func (m *MCPAgent) initializeMCPClient(ctx context.Context, serverName, version string, headers map[string]string) error {
	m.mcpConfig = config.NewMCPServerConfig()
	// 连接到mcp server
	m.mcpClient = tool.NewMCPClient(serverName, version, headers)
	if err := m.connectMCPServer(ctx); err != nil {
		return err
	}
	// 加载工具
	tools := m.mcpClient.Tools
	for k, v := range tools {
		m.tools[k] = v
	}
	return nil
}

func (m *MCPAgent) connectMCPServer(ctx context.Context) error {
	for k, v := range m.mcpConfig.McpServers {
		if v.Disabled {
			continue
		}
		switch v.Type {
		case "stdio":
			if err := m.mcpClient.ConnectStdio(ctx, k, v.Command, v.Args...); err != nil {
				return err
			}
		case "sse":
			if err := m.mcpClient.ConnectSSE(ctx, k, v.URL); err != nil {
				return err
			}
		case "streamableHttp":
			if err := m.mcpClient.ConnectStreamableHTTP(ctx, k, v.URL); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown server type: %s", v.Type)
		}
	}
	return nil
}

func (m *MCPAgent) GetTools() []schema.Tool {
	tools := make([]schema.Tool, 0, len(m.tools))
	for _, v := range m.tools {
		tools = append(tools, v.GetTool())
	}
	return tools
}

func (m *MCPAgent) GetToolChoice() schema.ToolChoice {
	return schema.ToolChoiceAuto
}

func (m *MCPAgent) ExecuteTool(ctx context.Context, toolCall schema.ToolCall) (schema.AgentState, string) {
	if toolCall.Function.Name == "" {
		return schema.AgentStateERROR, "Error: Invalid command format"
	}
	state := schema.AgentStateRUNNING
	for _, v := range m.specialToolNames {
		if toolCall.Function.Name == v {
			state = schema.AgentStateFINISHED
			break
		}
	}

	theTool, ok := m.tools[toolCall.Function.Name]
	if !ok {
		return schema.AgentStateERROR, fmt.Sprintf("Error: Unknown tool %s", toolCall.Function.Name)
	}

	var arguments map[string]any
	if toolCall.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &arguments); err != nil {
			return schema.AgentStateERROR, fmt.Sprintf("failed to parse arguments: %v", err)
		}
	}
	result, err := theTool.Execute(ctx, arguments)
	if err != nil {
		return schema.AgentStateERROR, fmt.Sprintf("Error: %s", err.Error())
	}
	return state, result
}

func (m *MCPAgent) Cleanup() {
	for k := range m.mcpConfig.McpServers {
		if err := m.mcpClient.Disconnect(k); err != nil {
			fmt.Printf("errors disconnecting from server %s: %v\n", k, err)
			continue
		}
	}
}
