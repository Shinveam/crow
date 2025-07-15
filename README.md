<div style="text-align:center">
  <img src="assets/logo.png" width="200" />
</div>

[![Golang 1.24.2+](https://img.shields.io/badge/Golang-1.24.2+-blue.svg)](https://www.python.org/downloads/) [![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)  ![Alpha](https://img.shields.io/badge/Status-Alpha-red)

 中文 | [English](README_en.md) 

## 🐦‍⬛ Crow - 智能语音助手服务

**如乌鸦般智慧灵巧，用声音连接世界**  

Crow 是一个可扩展的开源语音助手服务（AI Agent），支持语音对话、大模型函数调用（Function Calling）和自定义 MCP（模型上下文协议）服务调度，为开发者提供语音驱动的 AI Agent 实现思路。

### ✨ 核心特性

- **语音对话**：支持实时语音识别与合成，赋予 AI 自然交互的能力。

- **大模型函数调用**：支持大模型函数调用，支持自定义函数。

- **自定义 MCP 服务调度**：支持自定义 MCP 服务调度。

### 🤔 为什么命名 Crow

Crow，即「乌鸦」，乌鸦是自然界中少有的能使用工具、拥有高级智慧的鸟类，在本项目中寓意：

- **能说**：使用实时语音识别和合成，实现语音对话，如乌鸦般自然。

- **聪慧**：整合大模型实现复杂决策，如乌鸦般机智。

- **协作**：通过大模型函数调用（Function Calling）和 MCP 服务调度，实现工具协同。

## 👀 项目演示

![demo](assets/demo.gif)

## 🚀 快速开始

本项目基于 go 1.24.2 版本开发，使用前须提前准备好对应的 go 语言环境。

1. 克隆仓库

```bash
git clone https://github.com/Shinveam/crow.git
cd crow
```

2. 初始化项目

```bash
go mod tidy
```

3. 项目配置
   - 按[项目配置说明](config/README.md)👈进行配置。

> 该项目提供了两种启动方式，完整的体验语音交互须使用方式一。

### 1️⃣ 方式一：启用 websocket 服务

```bash
go run cmd/ws-server/main.go
```

> 本项目提供了 html 和 python 的客户端示例（位于 `example/`），运行对应客户端可完整体验项目效果。

### 2️⃣ 方式二：启用命令行交互

```bash
go run cmd/agent-cli/main.go
```

## 🔌 客户端对接

### 🌐 接入说明

#### 1. 接入方式

   - **Scheme**：websocket

   - **Port**：28080（可在配置文件中修改）

   - **Path**：/crow/v1

#### 2. 接入流程

   1. 客户端与服务端连接后，须发送消息类型为文本（opcode = 1）的 hello 消息（详看下方 hello 请求），发送完成后服务端会下发 hello 的确认消息，表示任务启动成功，可以开始后面的交互；

   2. 客户端可以采用文本的和音频的方式进行交互，若采用文本，须发送消息类型为文本的 chat 消息（详看下方 chat 请求）；
    
   3. 若采用语音交互，须上传待识别音频，消息类型设置为字节（opcode = 2），消息内容为音频块数据；

   4. 服务端消息**均采用文本类型**下发，内容为 JSON 序列化后的数据，详看下方参数说明；

#### 3. 参数说明

<details>
<summary><strong>1. hello 请求（点击展开）</strong></summary>

> **功能描述**：设置任务参数并启动任务  
> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

|          参数名           |   类型   |              描述              | 是否必填 |   默认值    |
|:----------------------:|:------:|:----------------------------:|:----:|:--------:|
|          type          | string |          固定为 hello           |  是   |    无     |
|       enable_asr       |  bool  |           是否启用ASR            |  否   |  false   |
|       enable_tts       |  bool  |           是否启用TTS            |  否   |  false   |
|       asr_params       | object | ASR设置参数（enable_asr为true时生效）  |  否   |    无     |
|   asr_params.format    | string |           待识别音频格式            |  否   |   pcm    |
| asr_params.sample_rate |  int   |        待识别音频采样率，单位：Hz        |  否   |  16000   |
|  asr_params.channels   |  int   |     待识别音频声道数，1：单声道，2：双声道     |  否   |    1     |
|   asr_params.vad_eos   |  int   |    语音活动检测（VAD）后端点时间，单位：毫秒    |  否   |   800    |
| asr_params.enable_punc |  bool  |           是否启用标点符号           |  否   |  false   |
|  asr_params.language   | string |      语种，如：zh（中文），en（英文）      |  否   |    zh    |
|   asr_params.accent    | string | 方言，mandarin：普通话；cantonese：粤语 |  否   | mandarin |
|       tts_params       | object | TTS设置参数（enable_tts为true时生效）  |  否   |    无     |
|   tts_params.speaker   | string |             发音人              |  否   |    无     |
|   tts_params.format    | string |           TTS音频格式            |  否   |   mp3    |
|    tts_params.speed    | float  |         语速：[0.5-2.0]         |  否   |   1.0    |
|   tts_params.volume    |  int   |          音量：[0-100]          |  否   |    50    |
|    tts_params.pitch    | float  |         语调：[0.5-2.0]         |  否   |   1.0    |
| tts_params.sample_rate |  int   |         音频采样率，单位：Hz          |  否   |  16000   |
|  tts_params.language   | string |      语种，如：zh（中文），en（英文）      |  否   |    zh    |

</details>

<details>
<summary><strong>2. hello 响应（点击展开）</strong></summary>

> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

|          参数名           |   类型   |              描述              | 是否必选 |
|:----------------------:|:------:|:----------------------------:|:----:|
|          type          | string |          固定为 hello           |  是   |
|       asr_params       | object | ASR设置参数（enable_asr为true时生效）  |  否   |
|   asr_params.format    | string |           待识别音频格式            |  否   |
| asr_params.sample_rate |  int   |        待识别音频采样率，单位：Hz        |  否   |
|  asr_params.channels   |  int   |     待识别音频声道数，1：单声道，2：双声道     |  否   |
|   asr_params.vad_eos   |  int   |    语音活动检测（VAD）后端点时间，单位：毫秒    |  否   |
| asr_params.enable_punc |  bool  |           是否启用标点符号           |  否   |
|  asr_params.language   | string |      语种，如：zh（中文），en（英文）      |  否   |
|   asr_params.accent    | string | 方言，mandarin：普通话；cantonese：粤语 |  否   |
|       tts_params       | object | TTS设置参数（enable_tts为true时生效）  |  否   |
|   tts_params.speaker   | string |             发音人              |  否   |
|   tts_params.format    | string |           TTS音频格式            |  否   |
|    tts_params.speed    | float  |         语速：[0.5-2.0]         |  否   |
|   tts_params.volume    |  int   |          音量：[0-100]          |  否   |
|    tts_params.pitch    | float  |         语调：[0.5-2.0]         |  否   |
| tts_params.sample_rate |  int   |         音频采样率，单位：Hz          |  否   |
|  tts_params.language   | string |      语种，如：zh（中文），en（英文）      |  否   |

</details>

<details>
<summary><strong>3. abort 请求（点击展开）</strong></summary>

> **功能描述**：可以在交互过程中发送该请求，用以打断当前的交互  
> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

| 参数名  |   类型   |    描述     | 是否必填 | 默认值 |
|:----:|:------:|:---------:|:----:|:---:|
| type | string | 固定为 abort |  是   | 无 |

</details>

<details>
<summary><strong>4. chat 请求（点击展开）</strong></summary>

> **功能描述**：请求文本  
> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

|    参数名    |   类型   |    描述    | 是否必填 | 默认值 |
|:---------:|:------:|:--------:|:----:|:---:|
|   type    | string | 固定为 chat |  是   |  无  |
| chat_text | string | 用户请求话术文本 |  是   |  无  |

</details>

<details>
<summary><strong>5. chat 响应（点击展开）</strong></summary>

> **功能描述**：响应文本，文本请求和语音请求都会返回该类型消息，用以返回答复话术  
> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

|    参数名     |   类型   |       描述        | 是否必选 |
|:----------:|:------:|:---------------:|:----:|
|    type    | string |    固定为 chat     |  是   |
|    text    | string |      答复话术       |  否   |

</details>

<details>
<summary><strong>6. asr 响应（点击展开）</strong></summary>

> **功能描述**：ASR 响应结果 
> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

|  参数名   |   类型   |             描述              | 是否必选 |
|:------:|:------:|:---------------------------:|:----:|
|  type  | string |           固定为 asr           |  是   |
| result | string |            识别结果             |  否   | 
| state  |  int   | 识别状态，0：识别中，1：单句识别结束，2：asr结束 |  否   |

</details>

<details>
<summary><strong>7. tts 响应（点击展开）</strong></summary>

> **功能描述**：TTS 响应结果
> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

|  参数名  |   类型   |        描述         | 是否必选 |
|:-----:|:------:|:-----------------:|:----:|
| type  | string |      固定为 tts      |  是   |
| audio | string |  base64 编码的音频数据   |  否   | 
| state |  int   | 识别状态，0：合成中，1：合成结束 |  否   |

</details>

<details>
<summary><strong>8. 错误响应（点击展开）</strong></summary>

> **功能描述**：错误响应  
> **消息类型**：文本（opcode = 1）  
> **消息格式**：JSON

|    参数名     |   类型   |       描述       | 是否必选 |
|:----------:|:------:|:--------------:|:----:|
|    type    | string |   固定为 error    |  是   |
| error_code |  int   | 错误码，0：正常，非0：异常 |  是   |
| error_msg  | string |      错误消息      |  否   |

</details>

### ⏱️ 时序图

![时序图](assets/timing.png)

## 📝 TODO

- [ ] 客户端请求认证；
- [ ] 后台管理界面；
- [ ] 视觉模型支持；

## 🙏 开源致谢

感谢 [OpenManus](https://github.com/FoundationAgents/OpenManus) 为本项目提供灵感和代码借阅。

## 🙋 贡献指南

欢迎提交 Issue 反馈问题或建议，或者通过 Pull Request 贡献您的代码，共同推动项目进步，一起成长！

## 🌟 星标历史

[//]: # ([![Star History Chart]&#40;https://api.star-history.com/svg?repos=Shinveam/crow&type=Date&#41;]&#40;https://star-history.com/#Shinveam/crow&Date&#41;)
