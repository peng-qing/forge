.PHONY: help proto gen

help:
	@echo "make proto  - 生成 api/proto 当前目录下所有协议的 Go 代码"
	@echo "make gen    - 生成 Proto Go 代码和 Command 胶水代码"
	@echo "./scripts/gen.sh [gencmd 参数] - 使用自定义生成参数"
	@echo "make help   - 显示帮助"

proto:
	./scripts/proto.sh

gen:
	./scripts/gen.sh
