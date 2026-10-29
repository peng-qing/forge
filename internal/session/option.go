package session

import "github.com/peng-qing/go_tools/common/options"

// WithOnError 设置错误处理接口
func WithOnError(onError ErrorHook) options.WrapperOptions[Router] {
	return func(router *Router) {
		router.onError = onError
	}
}

// WithObserver 设置观察接口
func WithObserver(observer ObserverHook) options.WrapperOptions[Router] {
	return func(router *Router) {
		router.observer = observer
	}
}
