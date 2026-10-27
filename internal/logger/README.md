# slog 糖水封装

这个包不定义自己的 Logger。默认 Logger、结构化字段、`With`、`WithGroup`、Context 方法等全部直接使用标准库 `*slog.Logger`。

本包只提供：

- 日志初始化和固定编码格式；
- `Debugf/Infof/Warnf/Errorf` 等格式化快捷函数；
- 动态日志级别；
- `go_tools/common/logger.LogFileRollover` 的 Flush、Rotate 和 Close 入口。

## 初始化

```go
err := logger.InitConfiguration("gateway", 1, &logger.Config{
    LogLevel:   "info",
    Format:     "text",
    LogDir:     "logs",
    Console:    true,
    MaxSize:    100,
    MaxBackups: 10,
    MaxAge:     7,
    Compress:   true,
})
if err != nil {
    return err
}
defer logger.Close()
```

初始化调用 `slog.SetDefault`，因此标准库入口和糖水函数使用同一个 Logger：

```go
slog.Info("gateway started", "addr", ":8080")
slog.Default().With("module", "session").Info("session attached", "uid", uid)

logger.Infof("player %d connected", uid)
logger.Errorf("listen failed: %v", err)
```

结构化日志直接使用 slog。只有需要 printf 格式时才使用本包的 `*f` 函数。不要通过本包再次封装 `Debug/Info/Warn/Error`。

`Panicf` 和 `Fatalf` 都按标准的 ERROR 级别记录，同时附加堆栈；前者随后 panic，后者 Flush 后调用 `os.Exit(1)`。本包不定义 DPanic、Panic、Fatal 等额外日志级别。

日志文件名为 `ALL_SPDLOG_gateway1.log`。文件切割、备份数量、保存时间和 gzip 压缩由 go_tools 的 `LogFileRollover` 直接负责，没有额外 Writer 包装。

时间格式为 `2024/06/11 10:00:00.000000`。Text 输出示例：

```text
time="2024/06/11 10:00:00.000000" level=INFO caller=/home/pengqq/go/src/forge/cmd/gateway/main.go:42 message="gateway started" addr=:8080
```

`SetLogLevel` 修改本包创建的 Handler 所共享的 `slog.LevelVar`：

```go
logger.SetLogLevel(logger.Debug)
logger.Debugf("connection %d opened", connID)
```

`Flush`、`Rotate`、`Close` 直接调用当前 `LogFileRollover`。包级 Close 通过清空当前 rollover 指针实现重复调用安全；关闭文件后应停止继续记录日志。
