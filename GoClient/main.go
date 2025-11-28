package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kardianos/service"
	"github.com/rs/zerolog"
)

const logRetentionDays = 15

var systemLogger service.Logger

type serviceProgram struct {
	logger     zerolog.Logger
	stopSignal chan struct{}
	stopOnce   sync.Once
}

func main() {
	workingDirectory, err := os.Getwd()
	if err != nil {
		log.Fatalf("获取工作目录失败: %v", err)
	}

	logWriter, err := newDailyLogWriter(workingDirectory, logRetentionDays)
	if err != nil {
		log.Fatalf("日志初始化失败: %v", err)
	}
	defer logWriter.Close()

	program := &serviceProgram{
		logger:     zerolog.New(logWriter).With().Timestamp().Logger(),
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
			//p.logInfo(fmt.Sprintf("心跳：%s", time.Now().Format(time.RFC3339)))

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
	p.logger.Info().Msg(message)
	if systemLogger != nil {
		systemLogger.Info(message)
	}
}

func (p *serviceProgram) logError(message string, err error) {
	if err != nil {
		p.logger.Error().Err(err).Msg(message)
		if systemLogger != nil {
			systemLogger.Error(err)
		}
	} else {
		p.logger.Error().Msg(message)
		if systemLogger != nil {
			systemLogger.Error(message)
		}
	}
}

type dailyLogWriter struct {
	dir        string
	retainDays int
	mu         sync.Mutex
	writer     *os.File
	currentDay string
}

func newDailyLogWriter(baseDirectory string, retainDays int) (*dailyLogWriter, error) {
	if retainDays < 1 {
		retainDays = 1
	}

	logDirectory := filepath.Join(baseDirectory, "logs")
	if err := os.MkdirAll(logDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	writer := &dailyLogWriter{
		dir:        logDirectory,
		retainDays: retainDays,
	}

	if err := writer.rotateIfNeeded(time.Now()); err != nil {
		return nil, err
	}

	return writer, nil
}

func (w *dailyLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.rotateIfNeeded(time.Now()); err != nil {
		return 0, err
	}

	return w.writer.Write(p)
}

func (w *dailyLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.writer != nil {
		return w.writer.Close()
	}

	return nil
}

func (w *dailyLogWriter) rotateIfNeeded(currentTime time.Time) error {
	today := currentTime.Format("2006-01-02")
	if w.writer != nil && w.currentDay == today {
		return nil
	}

	if w.writer != nil {
		w.writer.Close()
	}

	fileName := fmt.Sprintf("service-%s.log", today)
	filePath := filepath.Join(w.dir, fileName)
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	w.writer = file
	w.currentDay = today
	w.cleanupOldFiles(currentTime)

	return nil
}

func (w *dailyLogWriter) cleanupOldFiles(reference time.Time) {
	cutoff := reference.AddDate(0, 0, -w.retainDays)
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(w.dir, entry.Name()))
		}
	}
}
