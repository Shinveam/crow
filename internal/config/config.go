package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Mode string `yaml:"mode"`
		IP   string `yaml:"ip"`
		Port string `yaml:"port"`
	} `yaml:"server"`
	SelectedModule map[string]string    `yaml:"selected_module"`
	Asr            map[string]AsrConfig `yaml:"asr"`
	LLM            map[string]LLMConfig `yaml:"llm"`
	Tts            map[string]TtsConfig `yaml:"tts"`
	CMDExit        []string             `yaml:"cmd_exit"`
}

type AsrConfig struct {
	ApiKey string `yaml:"api_key"`
}

type LLMConfig struct {
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

type TtsConfig struct {
	ApiKey string `yaml:"api_key"`
}

var (
	config  *Config
	cfgLock sync.RWMutex
	once    sync.Once
)

func NewConfig() *Config {
	once.Do(func() {
		// 获取mcp server配置
		pwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		filePath := filepath.Join(pwd, "config", "config.yaml")
		if _, err = os.Stat(filePath); os.IsNotExist(err) {
			panic(fmt.Sprintf("config file not found: %s", filePath))
		}

		config = newConfig(filePath)
	})
	return config
}

func newConfig(configFilePath string) *Config {
	// 初始加载配置
	if err := loadConfig(configFilePath); err != nil {
		log.Fatalf("初始化配置失败: %v", err)
	}
	printConfig()

	go watchConfig(configFilePath)
	return config
}

func watchConfig(filePath string) {
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
		log.Fatalf("监听系统配置文件失败: %v", err)
	}

	fmt.Printf("开始监听系统配置文件变更: %s\n", filePath)

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
			log.Println("检测到系统配置文件变更，重新加载...")
			if err = loadConfig(filePath); err != nil {
				log.Printf("系统配置重载失败: %v", err)
			} else {
				printConfig()
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("系统配置监听错误: %v", err)
			break
		}
	}
}

func loadConfig(filename string) error {
	file, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("读取系统配置失败: %w", err)
	}
	var cfg Config
	if err = yaml.Unmarshal(file, &cfg); err != nil {
		return fmt.Errorf("解析系统配置失败: %w", err)
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	config = &cfg
	return nil
}

func printConfig() {
	cfgLock.RLock()
	defer cfgLock.RUnlock()

	fmt.Println("当前系统配置:")
	fmt.Printf("• 服务器模式: %s\n", config.Server.Mode)
	fmt.Printf("• 服务器IP: %s\n", config.Server.IP)
	fmt.Printf("• 服务器端口: %s\n", config.Server.Port)
	fmt.Println("• 已选择的模块:")
	for module, provider := range config.SelectedModule {
		fmt.Printf("  - %s: %s\n", module, provider)
	}
	fmt.Println("• ASR配置:")
	for name, cfg := range config.Asr {
		fmt.Printf("  - %s:\n", name)
		fmt.Printf("    api_key: %s\n", cfg.ApiKey)
	}
	fmt.Println("• LLM配置:")
	for name, cfg := range config.LLM {
		fmt.Printf("  - %s:\n", name)
		fmt.Printf("    model: %s\n", cfg.Model)
		fmt.Printf("    api_key: %s\n", cfg.APIKey)
		fmt.Printf("    base_url: %s\n", cfg.BaseURL)
	}
	fmt.Println("• TTS配置:")
	for name, cfg := range config.Tts {
		fmt.Printf("  - %s:\n", name)
		fmt.Printf("    api_key: %s\n", cfg.ApiKey)
	}
}
