package logger

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/peng-qing/go_tools/common/logger"
)

var (
	// 日志级别
	gLogLevel slog.LevelVar
	// 全局日志切割器
	gLogRollover atomic.Pointer[logger.LogFileRollover]
)

const (
	// json 格式
	JsonFormatter = "json"
	// text 格式
	TextFormatter = "text"
)

const (
	// 日志格式化
	logTimeFormat = "2006/01/02 15:04:05.000000"
)

type LogConfig struct {
	LogLevel   string `json:"log_level" yaml:"log_level"`     // LogLevel 日志级别
	Format     string `json:"format" yaml:"format"`           //  格式化方式
	LogDir     string `json:"log_dir" yaml:"log_dir"`         // 日志目录
	Console    bool   `json:"console" yaml:"console"`         // 是否同步输出到控制台
	AddSource  bool   `json:"add_source" yaml:"add_source"`   // 是否输出来源
	MaxSize    int    `json:"max_size" yaml:"max_size"`       // 单日志文件最大大小 单位MB
	MaxBackups int    `json:"max_backups" yaml:"max_backups"` // 最大日志文件流程数 0表示全留存
	MaxAge     int    `json:"max_age" yaml:"max_age"`         // 日志保留天数
	Compress   bool   `json:"compress" yaml:"compress"`       // 是否进行日志压缩
}

func init() {
	gLogLevel.Set(slog.LevelInfo)
	slog.SetDefault(slog.New(newHandler(os.Stdout, TextFormatter, false, &gLogLevel)))
}

// 创建 slog.Handler
func newHandler(writer io.Writer, format string, addSource bool, level slog.Leveler) slog.Handler {
	logConf := &slog.HandlerOptions{
		AddSource:   addSource,
		Level:       level,
		ReplaceAttr: replaceAttr,
	}

	switch strings.ToLower(strings.TrimSpace(format)) {
	case JsonFormatter:
		return slog.NewJSONHandler(writer, logConf)
	default:
		return slog.NewTextHandler(writer, logConf)
	}
}

// 属性命名替换
func replaceAttr(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Key {
	case slog.TimeKey:
		if attr.Value.Kind() == slog.KindTime {
			attr.Value = slog.StringValue(attr.Value.Time().Format(logTimeFormat))
		}
	}
	return attr
}

// InitConfiguration 根据配置初始日志信息
func InitConfiguration(prefix string, attrs map[string]any, logConf *LogConfig) error {
	if logConf == nil {
		return errors.New("logger: invalid logger configuration")
	}
	// 解析日志格式
	if err := gLogLevel.UnmarshalText([]byte(logConf.LogLevel)); err != nil {
		return err
	}
	// 文件输出
	filename := filepath.Join(logConf.LogDir, fmt.Sprintf("%s.log", prefix))
	logRollover := logger.NewLogFileRollover(filename, logConf.MaxSize, logConf.MaxBackups, logConf.MaxAge, logConf.Compress)
	var logWriter io.Writer = logRollover
	if logConf.Console {
		// 同步输出中断
		logWriter = io.MultiWriter(logRollover, os.Stdout)
	}
	// 日志器
	log := slog.New(newHandler(logWriter, logConf.Format, logConf.AddSource, &gLogLevel))
	if len(attrs) > 0 {
		args := make([]any, 0, len(attrs)*2)
		for key, value := range attrs {
			args = append(args, key, value)
		}
		log = log.With(args...)
	}
	slog.SetDefault(log)

	if prevRollover := gLogRollover.Swap(logRollover); prevRollover != nil {
		if err := prevRollover.Close(); err != nil {
			return fmt.Errorf("logger: close previous rollover: %w", err)
		}
	}

	return nil
}

// SetLogLevel 设置日志级别
func SetLogLevel(levelStr string) error {
	return gLogLevel.UnmarshalText([]byte(levelStr))
}

// GetLogLevel 获取当前日志级别
func GetLogLevel() string {
	return gLogLevel.Level().String()
}

// Flush 刷新日志文件写入
func Flush() error {
	if logRollover := gLogRollover.Load(); logRollover != nil {
		return logRollover.Sync()
	}
	return nil
}

// Rotate 主动触发一次日志轮转
func Rotate() error {
	if logRollover := gLogRollover.Load(); logRollover != nil {
		return logRollover.Rotate()
	}
	return nil
}

// Close 关闭日志写入
func Close() error {
	if logRollover := gLogRollover.Swap(nil); logRollover != nil {
		return logRollover.Close()
	}
	return nil
}
