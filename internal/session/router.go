package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"forge/api/pb"
	"forge/internal/core"

	"github.com/peng-qing/go_tools/common/container"
	"github.com/peng-qing/go_tools/common/network"
	"github.com/peng-qing/go_tools/common/options"
)

var (
	// 检查实现 network.Receiver[P] 接口
	_ network.Receiver[*pb.Message] = (*Router)(nil)
)

var (
	ErrInvalidMessage = errors.New("router: invalid message")        // 无效消息
	ErrInvalidKind    = errors.New("router: invalid message kind")   // 无效消息类型
	ErrCommandMissing = errors.New("router: command not registered") // 消息命令未注册
	ErrCommandPaused  = errors.New("router: command paused")         // 消息命令处理暂停
)

// Command 是 Router 调用的同意消息处理接口
type Command interface {
	// Execute 执行一条协议
	// 1. 将 Message.Payload 解析为具体的结构
	// 2. 调用规则生成的业务 Handler 入口
	// 3. 序列化响应并发送
	Execute(ctx context.Context, conn network.Connection[*pb.Message], msg *pb.Message) error
}

// ErrorHook 接收 Router 执行过程中的错误
// 回调执行应该及时返回避免阻塞连接
type ErrorHook func(ctx context.Context, conn network.Connection[*pb.Message], msg *pb.Message, err error)

// ObserverHook 接收实际执行过的命令统计
// 非法消息、未注册消息不回触发钩子
type ObserverHook func(cmd pb.CommandID, elapsed time.Duration, err error)

// Router 框架路由实现
type Router struct {
	commands      map[pb.CommandID]Command     // 注册的协议命令
	paused        *container.Set[pb.CommandID] // 标记停止的协议命令
	idBegin       pb.CommandID                 // 合法协议号开始
	idEnd         pb.CommandID                 // 合法协议号结束
	slowThreshold time.Duration                // 超时告警时间
	onError       ErrorHook                    // 错误钩子
	observer      ObserverHook                 // 统计钩子
	mutex         sync.RWMutex                 // 锁
}

// NewRouter 构造函数
func NewRouter(idBegin, idEnd pb.CommandID, slowThreshold time.Duration, opts ...options.Option[Router]) *Router {
	if idBegin > idEnd {
		idBegin, idEnd = idEnd, idBegin
	}
	router := &Router{
		commands:      make(map[pb.CommandID]Command),
		paused:        container.NewSet[pb.CommandID](),
		idBegin:       idBegin,
		idEnd:         idEnd,
		slowThreshold: slowThreshold,
	}

	for _, opt := range opts {
		opt.Apply(router)
	}

	return router
}

// Register 注册业务命令
// 注册一般在启动阶段 所以重复注册、空Command、越界协议号直接认为错误注册panic即可
func (r *Router) Register(cmdId pb.CommandID, command Command) {
	if command == nil {
		panic(fmt.Errorf("router: nil command register, cmdId:%d", cmdId))
	}
	if cmdId < r.idBegin || cmdId > r.idEnd {
		panic(fmt.Errorf("router: command id outside range, cmdId:%d, idBegin:%d, idEnd:%d", cmdId, r.idBegin, r.idEnd))
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, ok := r.commands[cmdId]; ok {
		panic(fmt.Errorf("router: duplicate command, cmdId:%d", cmdId))
	}

	r.commands[cmdId] = command
}

// Pause 暂停后续到达的命令处理 不影响已经进入 Execute 的命令
func (r *Router) Pause(cmdId pb.CommandID) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, ok := r.commands[cmdId]; !ok {
		return ErrCommandMissing
	}

	r.paused.Add(cmdId)
	return nil
}

// Recover 回复指定命令的处理 恢复后可以正常调用
func (r *Router) Recover(cmdId pb.CommandID) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if _, ok := r.commands[cmdId]; !ok {
		return ErrCommandMissing
	}

	r.paused.Remove(cmdId)
	return nil
}

// OnMessage 实现 network.Receiver[*pb.Message]
// 处理消息必须及时返回 否早会阻塞网络层的处理
func (r *Router) OnMessage(ctx context.Context, conn network.Connection[*pb.Message], msg *pb.Message) {
	if ctx.Err() != nil {
		return
	}
	if err := r.dispatch(ctx, conn, msg); err != nil {
		if r.onError != nil {
			r.onError(ctx, conn, msg, err)
		}
	}
}

// dispatch 执行具体的消息分发实现
func (r *Router) dispatch(ctx context.Context, conn network.Connection[*pb.Message], msg *pb.Message) error {
	if msg == nil || msg.Header == nil {
		slog.Error("[Router] dispatch invalid message or msg header")
		return ErrInvalidMessage
	}
	header := msg.Header
	command, paused, exists := r.lookup(header.CmdId)
	if !exists {
		return ErrCommandMissing
	}
	if paused {
		return ErrCommandPaused
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	startAt := time.Now()
	err := r.executeCommand(ctx, conn, msg, header.CmdId, command)
	elapsed := time.Since(startAt)

	if r.slowThreshold > 0 && elapsed > r.slowThreshold {
		slog.Warn("[Router] dispatch execute command slow",
			slog.Duration("elapsed", elapsed),
			slog.Duration("slowThreshold", r.slowThreshold),
			slog.Int64("cmdId", int64(header.CmdId)),
		)
	}

	// 处理完成钩子
	if r.observer != nil {
		r.observer(header.CmdId, elapsed, err)
	}

	return err
}

// lookup 获取命令相关信息
func (r *Router) lookup(cmdId pb.CommandID) (command Command, paused bool, exists bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	command, exists = r.commands[cmdId]
	if !exists {
		return nil, false, false
	}
	paused = r.paused.Contains(cmdId)

	return command, paused, exists
}

// executeCommand 执行命令
func (r *Router) executeCommand(ctx context.Context, conn network.Connection[*pb.Message], msg *pb.Message, cmdId pb.CommandID, command Command) (err error) {
	defer func() {
		if val := recover(); val != nil {
			err = &core.PanicError{
				Key:   strconv.Itoa(int(cmdId)),
				Value: val,
				Stack: debug.Stack(),
			}
		}
	}()

	// 执行命令
	if err := command.Execute(ctx, conn, msg); err != nil {
		return err
	}

	return nil
}
