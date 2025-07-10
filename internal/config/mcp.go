package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type McpServerConfig struct {
	Type     string   `json:"type"`
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	URL      string   `json:"url,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

type McpConfig struct {
	McpServers map[string]McpServerConfig `json:"mcpServers"`
}

var (
	mcpConfig  *McpConfig
	mcpCfgLock sync.RWMutex
	mcpOnce    sync.Once
)

func NewMCPServerConfig() *McpConfig {
	mcpOnce.Do(func() {
		// 获取mcp server配置
		pwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		filePath := filepath.Join(pwd, "config", "mcp_server_setting.json")
		if _, err = os.Stat(filePath); os.IsNotExist(err) {
			panic(fmt.Sprintf("config file not found: %s", filePath))
		}

		mcpConfig = newMCPServerConfig(filePath)
	})
	return mcpConfig
}

func newMCPServerConfig(configFilePath string) *McpConfig {
	// 初始加载配置
	if err := loadMCPConfig(configFilePath); err != nil {
		log.Fatalf("初始化配置失败: %v", err)
	}
	printMCPConfig()
	go watchMcpConfig(configFilePath)
	return mcpConfig
}

func watchMcpConfig(filePath string) {
	// 创建文件监听器
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		_ = watcher.Close()
	}()

	// 添加配置文件到监听列表
	if err = watcher.Add(filePath); err != nil {
		log.Fatalf("监听MCP文件失败: %v", err)
	}

	fmt.Printf("开始监听MCP配置文件变更: %s\n", filePath)

	// 处理文件变更事件（带防抖）
	debounceTimer := time.NewTimer(0)
	<-debounceTimer.C // 立即消耗初始信号

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// 只处理写入和重命名事件
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Rename) {
				debounceTimer.Reset(500 * time.Millisecond) // 500ms防抖
			}
		case <-debounceTimer.C:
			log.Println("检测到MCP配置文件变更，重新加载...")
			if err = loadMCPConfig(filePath); err != nil {
				log.Printf("MCP配置重载失败: %v", err)
			} else {
				printMCPConfig()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("MCP监听错误: %v", err)
			break
		}
	}
}

func loadMCPConfig(filename string) error {
	file, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("读取MCP配置失败: %w", err)
	}

	var newConfig McpConfig
	if err = json.Unmarshal(file, &newConfig); err != nil {
		return fmt.Errorf("解析JSON失败: %w", err)
	}

	mcpCfgLock.Lock()
	defer mcpCfgLock.Unlock()
	mcpConfig = &newConfig
	return nil
}

// 打印当前配置（线程安全）
func printMCPConfig() {
	mcpCfgLock.RLock()
	defer mcpCfgLock.RUnlock()

	fmt.Println("当前MCP Server配置:")
	for name, server := range mcpConfig.McpServers {
		fmt.Printf("• 服务器: %s\n", name)
		fmt.Printf("  类型: %s\n", server.Type)
		if server.Command != "" {
			fmt.Printf("  命令: %s %v\n", server.Command, server.Args)
		}
		if server.URL != "" {
			fmt.Printf("  URL: %s\n", server.URL)
		}
		fmt.Printf("  Disabled: %v\n", server.Disabled)
	}
}
