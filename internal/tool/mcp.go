package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"crow/internal/schema"
)

// MCPClientTool MCP 客户端可调用的工具
type MCPClientTool struct {
	client *client.Client
	tool   schema.Tool
}

func NewMCPClientTool(client *client.Client, tool schema.Tool) *MCPClientTool {
	return &MCPClientTool{client: client, tool: tool}
}

func (m *MCPClientTool) GetName() string {
	return m.tool.Function.Name
}

func (m *MCPClientTool) GetTool() schema.Tool {
	return m.tool
}

func (m *MCPClientTool) Execute(ctx context.Context, arguments map[string]any) (string, error) {
	toolRequest := mcp.CallToolRequest{
		Request: mcp.Request{
			Method: "tools/call",
		},
	}
	toolRequest.Params.Name = m.tool.Function.Name
	toolRequest.Params.Arguments = arguments
	result, err := m.client.CallTool(ctx, toolRequest)
	if err != nil {
		return "", fmt.Errorf("call tool failed: %v", err)
	}
	if len(result.Content) == 0 {
		return "", nil
	}
	return result.Content[0].(mcp.TextContent).Text, nil
}

// MCPClient 连接到多个 MCP 服务器并通过 Model Context Protocol 管理可用工具的工具集合。
type MCPClient struct {
	// 初始化MCP客户端的参数
	serverName string
	version    string
	// 连接管理
	sessions      map[string]*client.Client // k: serverId, v: MCP connect client
	session2Tools map[string][]string       // k: serverId, v: list of tool's name
	// 获取到的MCP Server的必要数据
	Prompts map[string]string // k: serverId, v: prompt that is formatted
	Tools   map[string]Caller // k: tool's name, v: MCPClientTool
}

func NewMCPClient(serverName, version string) *MCPClient {
	return &MCPClient{
		serverName: serverName,
		version:    version,
		sessions:   make(map[string]*client.Client),
	}
}

func (m *MCPClient) ConnectStdio(ctx context.Context, serverId, command string, arguments ...string) error {
	if command == "" {
		return errors.New("server command is required")
	}
	if serverId == "" {
		serverId = command
	}
	if _, ok := m.sessions[serverId]; ok {
		if err := m.Disconnect(serverId); err != nil {
			return fmt.Errorf("failed to disconnect server %s: %v", serverId, err)
		}
	}
	mcpClient, err := client.NewStdioMCPClient(command, nil, arguments...)
	if err != nil {
		return fmt.Errorf("new stdio mcp client failed: %v", err)
	}
	m.sessions[serverId] = mcpClient
	return m.initialize(ctx, serverId)
}

func (m *MCPClient) ConnectSSE(ctx context.Context, serverId, serverUrl string) error {
	if serverUrl == "" {
		return errors.New("server url is required")
	}
	if serverId == "" {
		serverId = serverUrl
	}
	if _, ok := m.sessions[serverId]; ok {
		if err := m.Disconnect(serverId); err != nil {
			return fmt.Errorf("failed to disconnect server %s: %v", serverId, err)
		}
	}
	mcpClient, err := client.NewSSEMCPClient(serverUrl)
	if err != nil {
		return fmt.Errorf("new sse mcp client failed: %v", err)
	}
	m.sessions[serverId] = mcpClient
	return m.initialize(ctx, serverId)
}

func (m *MCPClient) ConnectStreamableHTTP(ctx context.Context, serverId, baseUrl string) error {
	if baseUrl == "" {
		return errors.New("base url is required")
	}
	if serverId == "" {
		serverId = baseUrl
	}
	if _, ok := m.sessions[serverId]; ok {
		if err := m.Disconnect(serverId); err != nil {
			return fmt.Errorf("failed to disconnect server %s: %v", serverId, err)
		}
	}
	mcpClient, err := client.NewStreamableHttpClient(baseUrl)
	if err != nil {
		return fmt.Errorf("new streamable http client failed: %v", err)
	}
	m.sessions[serverId] = mcpClient
	return m.initialize(ctx, serverId)
}

func (m *MCPClient) initialize(ctx context.Context, serverId string) error {
	if serverId == "" {
		return errors.New("server id is required")
	}
	mcpClient, ok := m.sessions[serverId]
	if !ok {
		return fmt.Errorf("serverId %s is not exists", serverId)
	}
	if err := mcpClient.Start(ctx); err != nil {
		return fmt.Errorf("mcp client start failed: %v", err)
	}

	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    m.serverName,
		Version: m.version,
	}
	initRequest.Params.Capabilities = mcp.ClientCapabilities{}

	// 初始化MCP客户端并连接到服务器
	initResult, err := mcpClient.Initialize(ctx, initRequest)
	if err != nil {
		return fmt.Errorf("initialize mcp client failed: %v", err)
	}

	if initResult.Capabilities.Tools != nil {
		if err = m.getTools(ctx, serverId); err != nil {
			return fmt.Errorf("get tools failed: %v", err)
		}
	}
	// if initResult.Capabilities.Prompts != nil {
	// 	if err = m.getPrompt(ctx, serverId); err != nil {
	// 		return err
	// 	}
	// }
	return nil
}

func (m *MCPClient) getTools(ctx context.Context, serverId string) error {
	if serverId == "" {
		return errors.New("server id is required")
	}
	mcpClient, ok := m.sessions[serverId]
	if !ok {
		return fmt.Errorf("serverId %s is not exists", serverId)
	}

	toolsRequest := mcp.ListToolsRequest{}
	toolList, err := mcpClient.ListTools(ctx, toolsRequest)
	if err != nil {
		return err
	}

	if m.Tools == nil {
		m.Tools = make(map[string]Caller, len(toolList.Tools))
	}
	if m.session2Tools == nil {
		m.session2Tools = make(map[string][]string)
	}

	for _, t := range toolList.Tools {
		tool := schema.Tool{
			Type: "function",
			Function: schema.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters: map[string]any{
					"type":       t.InputSchema.Type,
					"properties": t.InputSchema.Properties,
					"required":   t.InputSchema.Required,
				},
			},
		}
		m.Tools[t.Name] = NewMCPClientTool(mcpClient, tool)
		m.session2Tools[serverId] = append(m.session2Tools[serverId], t.Name)
	}
	return nil
}

func (m *MCPClient) getPrompt(ctx context.Context, serverId string) error {
	if serverId == "" {
		return errors.New("server id is required")
	}
	mcpClient, ok := m.sessions[serverId]
	if !ok {
		return fmt.Errorf("server id %s is not exists", serverId)
	}

	promptsRequest := mcp.ListPromptsRequest{}
	prompts, err := mcpClient.ListPrompts(ctx, promptsRequest)
	if err != nil {
		return err
	}

	if m.Prompts == nil {
		m.Prompts = make(map[string]string)
	}
	var formattedPrompt string
	for _, prompt := range prompts.Prompts {
		formattedPrompt += fmt.Sprintf("%s: %s\n", prompt.Name, prompt.Description)
		arguments, _ := json.MarshalIndent(&prompt.Arguments, "", "    ")
		if len(arguments) != 0 {
			formattedPrompt += fmt.Sprintf("参数:\n%v\n", string(arguments))
		}
	}
	if formattedPrompt != "" {
		m.Prompts[serverId] = formattedPrompt
	}
	return nil
}

func (m *MCPClient) Disconnect(serverId string) error {
	if serverId == "" {
		return errors.New("server id is required")
	}
	if mcpClient, ok := m.sessions[serverId]; ok {
		if err := mcpClient.Close(); err != nil {
			return fmt.Errorf("mcp client close failed: %v", err)
		}
	}
	delete(m.sessions, serverId)
	delete(m.Prompts, serverId)
	for _, toolName := range m.session2Tools[serverId] {
		delete(m.Tools, toolName)
	}
	return nil
}
