package framework

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"forge/internal/core"
)

// 当前没有容器配置结构，先集中定义排空期限。
const drainTimeout = 15 * time.Second

// ErrUnexpectedComponentExit 组件提前结束
var ErrUnexpectedComponentExit = errors.New("component exited unexpectedly")

// Container 主 goroutine             组件监督 goroutine
//         │                                  │
// 启动组件、继续启动其他组件           waiter.Wait() 阻塞
//         │                                  │
// 进入 waitForExit()                  组件运行结束
//         │                                  │
// 等待系统信号或退出通知              Wait() 返回
//         │                                  │
//         └────── 收到通知 ←────────── select 发送结果

// 组件退出原因
type componentExit struct {
	name string // 组件名
	err  error  // 错误
}

// 组件包装
type componentEntry struct {
	component Component // 组件
	started   bool      // 启动标记
}

// Container 容器管理
type Container struct {
	components []*componentEntry  // 组件列表
	finalizers []Finalizer        // 析构列表
	exits      chan componentExit // 组件退出结果
	wg         sync.WaitGroup     // 等待组
}

// NewContainer 构造函数 创建容器
func NewContainer() *Container {
	return &Container{exits: make(chan componentExit, 1)}
}

// AddComponent 按照启动顺序注册到容器 关闭时会逆序Close
func (c *Container) AddComponent(components ...Component) {
	for _, addComponent := range components {
		if addComponent == nil {
			continue
		}
		c.components = append(c.components, &componentEntry{
			component: addComponent,
			started:   false,
		})
	}
}

// AddFinalizer 注册清理函数 在组件释放完成后逆序执行
func (c *Container) AddFinalizer(finalizers ...Finalizer) {
	for _, addFinalizer := range finalizers {
		if addFinalizer == nil {
			continue
		}
		c.finalizers = append(c.finalizers, addFinalizer)
	}
}

// Serve 启动注册组件开始对外暴露服务
func (c *Container) Serve() error {
	// 启动组件
	runErr := c.runComponents()
	if runErr == nil {
		// 组件启动成功 等待退出信号
		runErr = c.waitForExit()
	}

	// 释放相关组件
	drainErr := c.drainComponents() // 优雅排空 可选
	closeErr := c.closeComponents() // 执行关闭

	// 确保容器创建的监督任务退出
	c.wg.Wait()

	// 执行进程析构
	finalizeErr := c.runFinalizer()

	return errors.Join(runErr, drainErr, closeErr, finalizeErr)
}

// runComponents 顺序执行所有的注册组件
func (c *Container) runComponents() error {
	for index, entry := range c.components {
		if err := entry.component.Start(); err != nil {
			// 启动失败组件需要自己处理相关资源释放
			slog.Error("[Container] runComponents components start failed",
				slog.Int("index", index),
				slog.String("name", entry.component.Name()),
				slog.Any("err", err),
			)
			return err
		}
		entry.started = true
		// 如果是阻塞类接口 启动监督任务等待其退出
		if waiter, ok := entry.component.(Waiter); ok {
			c.watchWaiterComponent(entry.component.Name(), waiter)
		}
	}

	return nil
}

// watchWaiterComponent 观察阻塞任务组件退出
func (c *Container) watchWaiterComponent(name string, waiter Waiter) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		// 等待组件退出
		waitErr := func() (err error) {
			defer func() {
				if cause := recover(); cause != nil {
					err = &core.PanicError{
						Key:   "Container." + name + ".Wait",
						Value: cause,
						Stack: debug.Stack(),
					}
				}
			}()

			return waiter.Wait()
		}()

		if waitErr != nil {
			slog.Error("[Container] watchWaiterComponent waiter exit failed", slog.String("name", name), slog.Any("err", waitErr))
		}

		// 一个退出结果就足以触发统一关闭
		result := componentExit{
			name: name,
			err:  waitErr,
		}

		// 关闭过程中其他组件的退出不应阻塞监督任务
		select {
		case c.exits <- result:
		default:
		}
	}()
}

// waitForExit 阻塞等待相关的退出信号
func (c *Container) waitForExit() error {
	signals := make(chan os.Signal, 1)
	// SIGINT 键盘中断
	// SIGTERM 终止信号
	// SIGQUIT 键盘退出键按下
	// SIGTRAP 内存异常发送给进程的信号
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGTRAP)
	defer signal.Stop(signals)

	select {
	case sig := <-signals:
		// 系统推出信号
		slog.Warn("[Container] waitForExit received exit signal", slog.String("signal", sig.String()))
		return nil
	case result := <-c.exits:
		// 组件推出信号
		slog.Error("[Container] waitForExit component exited unexpectedly", slog.String("name", result.name), slog.Any("err", result.err))
		if result.err != nil {
			return result.err
		}
		return ErrUnexpectedComponentExit
	}
}

// runFinalizer 逆序执行注册的析构
func (c *Container) runFinalizer() error {
	var results []error
	for index := len(c.finalizers) - 1; index >= 0; index-- {
		finalizer := c.finalizers[index]
		if err := finalizer(); err != nil {
			slog.Error("[Container] runFinalizer failed",
				slog.Int("index", index),
				slog.Any("err", err),
			)
			results = append(results, err)
		}
	}

	return errors.Join(results...)
}

// drainComponents 并发排空已经成功启动的组件
func (c *Container) drainComponents() error {
	ctx, ctxCancel := context.WithTimeout(context.Background(), drainTimeout)
	defer ctxCancel()

	var wg sync.WaitGroup
	results := make([]error, len(c.components)+1)

	for index, entry := range c.components {
		if !entry.started {
			continue
		}
		drainer, ok := entry.component.(Drainer)
		if !ok {
			continue
		}
		wg.Add(1)

		// 异步排空
		go func(index int, name string, drainer Drainer) {
			defer wg.Done()

			drainErr := func() (err error) {
				defer func() {
					if cause := recover(); cause != nil {
						err = &core.PanicError{
							Key:   "Container." + name + ".Drain",
							Value: cause,
							Stack: debug.Stack(),
						}
					}
				}()

				return drainer.Drain(ctx)
			}()

			if drainErr != nil {
				slog.Error(
					"[Container] drainComponents drain failed",
					slog.Int("index", index),
					slog.String("name", name),
					slog.Any("err", drainErr),
				)
			}

			results[index] = drainErr
		}(index, entry.component.Name(), drainer)
	}

	// 等待排空完成
	wg.Wait()

	// 如果 ctx 退出
	if err := ctx.Err(); err != nil {
		slog.Error("[Container] drainComponents drain ctx canceled",
			slog.Int("componentCount", len(c.components)),
			slog.Any("err", err),
		)
		results = append(results, err)
	}

	return errors.Join(results...)
}

// closeComponents 逆序关闭已经成功启动的组件
func (c *Container) closeComponents() error {
	var results []error

	for index := len(c.components) - 1; index >= 0; index-- {
		entry := c.components[index]
		if !entry.started {
			continue
		}
		if err := entry.component.Close(); err != nil {
			slog.Error("[Container] closeComponents failed",
				slog.Int("index", index),
				slog.String("name", entry.component.Name()),
				slog.Any("err", err),
			)
			results = append(results, err)
		}
	}

	return errors.Join(results...)
}
