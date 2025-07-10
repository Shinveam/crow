# 配置说明

## 系统配置

在`config.yaml`文件中进行系统配置。  

当前`paraformer`、`qwen`、`cosy_voice`的相关配置均来自于[阿里云百炼平台](https://www.aliyun.com/product/bailian)，程序运行前，请先到该平台获取相关信息并填入到对应配置中。

## MCP 服务配置

在`mcp_server_settings.json`文件中配置 mcp 服务，具体配置参考如下示例。

### 示例

```josn
{
  "mcpServers": {                                    // 固定为 mcpServers
    "example-stdio": {                               // mcp 服务名
      "type": "stdio",                               // mcp 服务类型，stdio|sse|streamableHttp，必填
      "command": "python",                           // 启动工具的命令，stdio 类型的必填
      "args": ["-m", "local_module", "--port=8000"], // 启动参数，stdio 类型的选填
      "disabled": false                              // 是否禁用，默认 false
    },
    "example-sse": {
      "type": "sse",
      "url": "https://your-domain.com/sse-endpoint", // sse|streamableHttp 服务端点，sse|streamableHttp 类型的必填
      "disabled": true
    }
  }
}
```
