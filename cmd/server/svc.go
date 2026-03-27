package main

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"sync"
	"xiaozhi-esp32-server-golang/internal/app/server"
	user_config "xiaozhi-esp32-server-golang/internal/domain/config"
	log "xiaozhi-esp32-server-golang/logger"

	"github.com/kardianos/service"
	"github.com/spf13/viper"
)

// ServiceConfig 服务配置，由 main 通过 flag 填充
type ServiceConfig struct {
	ConfigFile    string
	ManagerEnable bool
	ManagerConfig string
	AsrEnable     bool
	AsrConfig     string
}

// program 实现 service.Interface，作为系统服务的入口
type program struct {
	cfg    ServiceConfig
	app    *server.App
	stopCh chan struct{}
	lock   sync.RWMutex
}

// Start 由服务管理器调用，必须立即返回（非阻塞）
func (p *program) Start(s service.Service) error {
	p.stopCh = make(chan struct{})
	go p.run()
	return nil
}

// run 在 goroutine 中执行真正的启动逻辑
func (p *program) run() {
	if p.cfg.ManagerEnable {
		StartManagerHTTP(p.cfg.ManagerConfig)
	}
	if p.cfg.AsrEnable {
		StartAsrServerHTTP(p.cfg.AsrConfig)
	}

	if err := Init(p.cfg.ConfigFile); err != nil {
		log.Errorf("初始化失败: %v", err)
		return
	}

	if viper.GetBool("server.pprof.enable") {
		pprofPort := viper.GetInt("server.pprof.port")
		go func() {
			log.Infof("启动 pprof 服务，端口: %d", pprofPort)
			if err := http.ListenAndServe(fmt.Sprintf(":%d", pprofPort), nil); err != nil {
				log.Errorf("pprof 服务启动失败: %v", err)
			}
		}()
		log.Infof("pprof 地址: http://localhost:%d/debug/pprof/", pprofPort)
	}

	p.app = server.NewApp()

	user_config.RegisterManagerSystemConfigHandler(func(data map[string]interface{}) {
		p.lock.Lock()
		defer p.lock.Unlock()

		current := viper.AllSettings()
		oldMqttServer := current["mqtt_server"]
		oldMqtt := current["mqtt"]
		oldUdp := current["udp"]
		oldMcp := current["mcp"]
		oldLocalMcp := current["local_mcp"]

		var doMqttServer, doMqttReload, doUdpReload, doMcpReload bool
		if data["mqtt_server"] != nil && !SystemConfigEqual(data["mqtt_server"], oldMqttServer) {
			doMqttServer = true
		}
		if data["mqtt"] != nil && !SystemConfigEqual(data["mqtt"], oldMqtt) {
			doMqttReload = true
		}
		if data["udp"] != nil && udpListenChanged(data["udp"], oldUdp) {
			doUdpReload = true
		}
		if data["mcp"] != nil && !SystemConfigEqual(data["mcp"], oldMcp) {
			doMcpReload = true
		}
		if data["local_mcp"] != nil && !SystemConfigEqual(data["local_mcp"], oldLocalMcp) {
			doMcpReload = true
		}

		ApplySystemConfigToViper(data)

		var wg sync.WaitGroup
		if doMqttServer {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.app.ReloadMqttServer()
			}()
		}
		if doMqttReload || doUdpReload {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.app.ReloadMqttUdpWithFlags(doMqttReload, doUdpReload)
			}()
		}
		if doMcpReload {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := p.app.ReloadMCP(); err != nil {
					log.Errorf("ReloadMCP failed: %v", err)
				}
			}()
		}
		wg.Wait()
	})

	p.app.Run()
}

// Stop 由服务管理器调用，执行优雅关闭
func (p *program) Stop(s service.Service) error {
	log.Info("正在关闭服务器...")
	StopPeriodicConfigUpdate()
	if p.cfg.ManagerEnable {
		StopManagerHTTP()
	}
	if p.cfg.AsrEnable {
		StopAsrServerHTTP()
	}
	if p.stopCh != nil {
		close(p.stopCh)
	}
	log.Info("服务器已关闭")
	return nil
}

// newService 构建 service.Service 实例
func newService(cfg ServiceConfig) (service.Service, *program, error) {
	prg := &program{cfg: cfg}

	// 以二进制所在目录作为服务的工作目录，保证相对路径（如 manager.json）始终可寻址
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	workDir := filepath.Dir(execPath)

	svcCfg := &service.Config{
		Name:        "XiaozhiServer",
		DisplayName: "Xiaozhi ESP32 Server",
		Description: "Xiaozhi ESP32 智能语音服务器",
		// 将启动参数写入服务注册信息，保证 install 后自动启动时带上同样的 flag
		Arguments: buildServiceArgs(cfg),
		Option: service.KeyValue{
			// systemd: WorkingDirectory=<binary所在目录>
			// Windows SCM: AppDirectory
			"WorkingDirectory": workDir,
		},
	}

	s, err := service.New(prg, svcCfg)
	return s, prg, err
}

// buildServiceArgs 根据当前 flag 值构造注册到系统服务的启动参数列表
func buildServiceArgs(cfg ServiceConfig) []string {
	var args []string
	if cfg.ConfigFile != "" && cfg.ConfigFile != defaultConfigFilePath {
		args = append(args, "-c", cfg.ConfigFile)
	}
	if cfg.ManagerEnable != defaultManagerEnable {
		if cfg.ManagerEnable {
			args = append(args, "-manager-enable=true")
		} else {
			args = append(args, "-manager-enable=false")
		}
	}
	if cfg.ManagerConfig != "" {
		args = append(args, "-manager-config", cfg.ManagerConfig)
	}
	if cfg.AsrEnable != defaultAsrEnable {
		if cfg.AsrEnable {
			args = append(args, "-asr-enable=true")
		} else {
			args = append(args, "-asr-enable=false")
		}
	}
	if cfg.AsrConfig != "" {
		args = append(args, "-asr-config", cfg.AsrConfig)
	}
	return args
}
