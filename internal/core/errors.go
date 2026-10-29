package core

import "fmt"

var (
	// 检查自定义 error 接口
	_ error = (*PanicError)(nil)
)

// PanicError 保存业务中的 panic 堆栈信息
type PanicError struct {
	Key   string // key
	Value any    // 保存的必要信息
	Stack []byte // 堆栈
}

// Error 实现 Error 接口
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: key=%s, value=%v", e.Key, e.Value)
}
