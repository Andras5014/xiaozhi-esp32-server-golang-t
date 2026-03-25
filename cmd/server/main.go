package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/kardianos/service"
)

func main() {
	configFile := flag.String("c", defaultConfigFilePath, "配置文件路径")
	managerEnable := flag.Bool("manager-enable", defaultManagerEnable, "是否启用内嵌 manager")
	managerConfig := flag.String("manager-config", "", "manager 配置文件路径，启用时可选")
	asrEnable := flag.Bool("asr-enable", defaultAsrEnable, "是否启用内嵌 asr_server")
	asrConfig := flag.String("asr-config", "", "asr_server 配置文件路径，启用时可选")
	flag.Parse()

	if *configFile == "" {
		fmt.Println("配置文件路径不能为空")
		os.Exit(1)
	}

	cfg := ServiceConfig{
		ConfigFile:    *configFile,
		ManagerEnable: *managerEnable,
		ManagerConfig: *managerConfig,
		AsrEnable:     *asrEnable,
		AsrConfig:     *asrConfig,
	}

	s, _, err := newService(cfg)
	if err != nil {
		fmt.Printf("创建服务失败: %v\n", err)
		os.Exit(1)
	}

	// 处理服务控制命令：install / uninstall / start / stop / restart / status
	if args := flag.Args(); len(args) > 0 {
		cmd := args[0]
		if err := service.Control(s, cmd); err != nil {
			fmt.Printf("服务命令 %q 执行失败: %v\n", cmd, err)
			fmt.Println("可用命令: install | uninstall | start | stop | restart | status")
			os.Exit(1)
		}
		fmt.Printf("服务命令 %q 执行成功\n", cmd)
		return
	}

	// 无控制命令：直接运行（交互模式或被服务管理器调用时均适用）
	logger, err := s.SystemLogger(nil)
	if err == nil {
		_ = logger.Info("XiaozhiServer 正在启动...")
	}
	if err := s.Run(); err != nil {
		fmt.Printf("服务运行失败: %v\n", err)
		os.Exit(1)
	}
}

func udpListenChanged(newUdpCfg interface{}, oldUdpCfg interface{}) bool {
	newListenHost, newListenPort := udpListenHostPort(newUdpCfg)
	oldListenHost, oldListenPort := udpListenHostPort(oldUdpCfg)
	if newListenHost == "" && newListenPort == 0 {
		return false
	}
	return newListenHost != oldListenHost || newListenPort != oldListenPort
}

func udpListenHostPort(cfg interface{}) (string, int) {
	if cfg == nil {
		return "", 0
	}
	type udpListen struct {
		ListenHost string `json:"listen_host"`
		ListenPort int    `json:"listen_port"`
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", 0
	}
	var parsed udpListen
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", 0
	}
	return parsed.ListenHost, parsed.ListenPort
}
