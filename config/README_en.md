# Configuration Instructions

## System Configuration

System settings are configured in the `config.yaml` file.

The current configurations for `paraformer`, `qwen`, and `cosy_voice` are sourced from the [Alibaba Cloud Bailian Platform](https://www.aliyun.com/product/bailian), Before running the program, please visit the platform to obtain relevant information and fill it into the corresponding configurations.

## MCP Server Configuration

Configure MCP services in the `mcp_server_settings.json` file. Refer to the example below for specific configurations.

### Example

```josn
{
  "mcpServers": {                                    // This must be mcpServers (fixed)
    "example-stdio": {                               // MCP service name
      "type": "stdio",                               // MCP service type: stdio | sse | streamableHttp (required)
      "command": "python",                           // Command to start the tool (required for stdio)
      "args": ["-m", "local_module", "--port=8000"], // Launch arguments (optional for stdio)
      "disabled": false                              // Whether disabled (default: false)
    },
    "example-sse": {
      "type": "sse",
      "url": "https://your-domain.com/sse-endpoint", // Endpoint for sse | streamableHttp (required for sse | streamableHttp)
      "disabled": true
    }
  }
}
```
