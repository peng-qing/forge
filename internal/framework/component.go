package framework

import "context"

// Component 是所有生命周期组件必须实现的核心接口
type Component interface {
	// Name 返回进程内唯一且稳定的组件名称
	Name() string
	// Start 启动组件
	Start() error
	// Close 关闭组件拥有的全部资源
	Close() error
}

// Waiter 是可选的运行监督能力
type Waiter interface {
	// Wait 等待组件结束
	Wait() error
}

// Drainer 是可选的优雅排空能力
type Drainer interface {
	// Drain 停止接收新任务 等待在途任务完成
	Drain(ctx context.Context) error
}

// Finalizer 进程级设施释放清理 按注册顺序逆序 在全部组件释放后执行
type Finalizer func() error
