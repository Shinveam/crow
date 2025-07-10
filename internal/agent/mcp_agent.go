package agent

import (
	"context"
	"fmt"

	mcpconfig "crow/internal/config"
	"crow/internal/schema"
	tool2 "crow/internal/tool"
)

type MCPAgent struct {
	mcpConfig        *mcpconfig.McpConfig
	mcpClient        *tool2.MCPClient
	tools            map[string]tool2.Caller
	specialToolNames []string
}

func NewMCPAgent(ctx context.Context) (*MCPAgent, error) {
	terminateTool := tool2.NewTerminate()
	curTimeTool := tool2.NewCurrentTime()
	agent := &MCPAgent{
		tools: map[string]tool2.Caller{
			terminateTool.GetName(): terminateTool,
			curTimeTool.GetName():   curTimeTool,
		},
		specialToolNames: []string{terminateTool.GetName()},
	}
	err := agent.initializeMCPClient(ctx, "mcp", "1.0.0")
	if err != nil {
		return nil, err
	}
	return agent, nil
}

func (m *MCPAgent) initializeMCPClient(ctx context.Context, serverName, version string) error {
	m.mcpConfig = mcpconfig.NewMCPServerConfig()
	// 连接到mcp server
	m.mcpClient = tool2.NewMCPClient(serverName, version)
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

func (m *MCPAgent) GetMCPServerPrompt() string {
	var prompt string
	for _, v := range m.mcpClient.Prompts {
		prompt += v
	}
	return prompt
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
	result, err := theTool.Execute(ctx, toolCall.Function.Arguments)
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
