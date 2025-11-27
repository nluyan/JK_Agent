package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kardianos/service"
)

var systemLogger service.Logger

type serviceProgram struct {
	fileLogger *log.Logger
	stopSignal chan struct{}
	stopOnce   sync.Once
}

func main() {
	logWriter, err := createLogWriter()
	if err != nil {
		log.Fatalf("日志初始化失败: %v", err)
	}
	defer logWriter.Close()

	program := &serviceProgram{
		fileLogger: log.New(logWriter, "", log.LstdFlags),
		stopSignal: make(chan struct{}),
	}

	config := &service.Config{
		Name:        "JKAgentGoClientService",
		DisplayName: "JK Agent Go Client Service",
		Description: "Go 语言编写的 Windows 服务示例，负责持续运行并写入日志。",
	}

	svc, err := service.New(program, config)
	if err != nil {
		program.logError("创建服务失败", err)
		os.Exit(1)
	}

	systemLogger, err = svc.Logger(nil)
	if err != nil {
		program.logError("创建系统日志记录器失败", err)
	}

	if err := svc.Run(); err != nil {
		program.logError("服务运行失败", err)
		os.Exit(1)
	}
}

func (p *serviceProgram) Start(s service.Service) error {
	p.logInfo("服务收到启动请求")
	go p.run()
	return nil
}

func (p *serviceProgram) run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	p.logInfo("服务进入运行循环")
	for {
		select {
		case <-ticker.C:
			p.logInfo(fmt.Sprintf("心跳：%s", time.Now().Format(time.RFC3339)))
		case <-p.stopSignal:
			p.logInfo("服务停止信号被触发")
			return
		}
	}
}

func (p *serviceProgram) Stop(s service.Service) error {
	p.logInfo("服务收到停止请求")
	p.stopOnce.Do(func() {
		close(p.stopSignal)
	})
	return nil
}

func (p *serviceProgram) logInfo(message string) {
	p.safeLog("[INFO] " + message)
	if systemLogger != nil {
		systemLogger.Info(message)
	}
}

func (p *serviceProgram) logError(message string, err error) {
	if err != nil {
		p.safeLog("[ERROR] " + message + ": " + err.Error())
		if systemLogger != nil {
			systemLogger.Error(err)
		}
	} else {
		p.safeLog("[ERROR] " + message)
		if systemLogger != nil {
			systemLogger.Error(message)
		}
	}
}

func (p *serviceProgram) safeLog(entry string) {
	if p.fileLogger != nil {
		p.fileLogger.Println(entry)
	}
}

func createLogWriter() (io.WriteCloser, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("获取工作目录失败: %w", err)
	}

	logDirectory := filepath.Join(workingDirectory, "logs")
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	logFilePath := filepath.Join(logDirectory, "service.log")
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}

	return file, nil
}
