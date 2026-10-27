# 消息协议命名与 Command 代码生成规范

## 1. 目标

Forge 的所有网络消息使用同一套命名规则，覆盖客户端、Gateway、Logic、Scene 以及服务间通信。协议名称明确逻辑来源和最终处理方，协议号标识业务操作，请求和响应共用协议号。

本规范同时约束业务 Handler 的签名，使工具能够生成 Payload 解码、业务调用、响应编码和 Command 注册代码。

## 2. 服务代码

| 代码 | 服务 |
| --- | --- |
| `C` | Client |
| `G` | Gateway |
| `L` | Logic |
| `S` | Scene |

`S` 在 Forge 协议中只表示 Scene，不表示泛化的 Server。

消息名称表达逻辑来源和最终处理方，不表达实际经过的网络节点。例如客户端消息通过 Gateway 转发到 Logic，仍然命名为 `C2LLoginRequest`。

服务代码默认放在 `services/<服务名>` 下：

```text
services/
  gateway/                       Gateway 服务组装与业务代码
    handler/                     最终目标为 G 的 Handler
  logic/                         Logic 服务组装与业务代码
    handler/                     最终目标为 L 的 Handler
  scene/                         Scene 服务组装与业务代码
    handler/                     最终目标为 S 的 Handler
```

`services/<服务名>` 根包存放 `Service`、构建组装、生命周期和该服务的注册入口；`handler` 子包只存放到达该服务后的业务处理函数。

## 3. 消息结构命名

统一格式：

```text
<来源>2<目标><业务动作><消息类型>
```

消息类型固定为：

- `Request`：需要对端处理并返回结果。
- `Response`：与 Request 配对的处理结果。
- `Notify`：不需要业务响应的单向通知。

示例：

```proto
message C2LLoginRequest {}
message L2CLoginResponse {}

message C2SEnterSceneRequest {}
message S2CEnterSceneResponse {}

message L2SPlayerEnterRequest {}
message S2LPlayerEnterResponse {}

message G2LSessionClosedNotify {}
message S2CMonsterBornNotify {}
```

请求和响应必须满足以下配对条件：

1. 业务动作相同。
2. 来源和目标互换。
3. 后缀分别为 `Request` 和 `Response`。

例如 `C2LLoginRequest` 只能与 `L2CLoginResponse` 配对。

## 4. 协议号语义

`cmd_id` 标识业务操作，请求和响应使用同一个协议号：

```text
cmd_id = COMMAND_ID_C2L_LOGIN, kind = REQUEST  -> C2LLoginRequest
cmd_id = COMMAND_ID_C2L_LOGIN, kind = RESPONSE -> L2CLoginResponse
```

`MessageHeader.cmd_id` 直接使用 `CommandID` 枚举类型，Router 的注册表、暂停集合和观察回调也统一使用 `pb.CommandID`。生成代码直接注册枚举项：

```go
router.Register(pb.CommandID_COMMAND_ID_C2L_LOGIN, C2LLoginCommand{})
```

不再额外生成 `uint32` 类型的 `CmdC2LLogin` 常量。

协议号号段表示 Request 的处理服务：

```text
Gateway 号段：请求由 Gateway 处理
Logic 号段：请求由 Logic 处理
Scene 号段：请求由 Scene 处理
```

响应不再按号段发送到请求处理服务，而是根据连接、`request_id` 和 `session_id` 返回请求方。

通知拥有独立协议号，协议号按通知的目标服务或所属业务模块分配，具体方案由 CommandID 号段文档进一步规定。

协议号在 `api/proto/command.proto` 中统一定义。业务代码和生成器引用枚举项，不重复填写数字：

```proto
enum CommandID {
  COMMAND_ID_INVALID = 0;
  COMMAND_ID_C2L_LOGIN = 20001;
  COMMAND_ID_C2S_ENTER_SCENE = 30001;
}
```

CommandID 固定使用：

```text
COMMAND_ID_<来源>2<目标>_<大写下划线业务动作>
```

例如 `C2LLoginRequest` 自动对应 `COMMAND_ID_C2L_LOGIN`，不需要在 Handler 上重复标记协议号。

## 5. MessageKind

统一消息包通过 `kind` 表示交互语义：

```proto
enum MessageKind {
  MESSAGEKIND_UNSPECIFIED = 0;
  MESSAGEKIND_REQUEST = 1;
  MESSAGEKIND_RESPONSE = 2;
  MESSAGEKIND_NOTIFY = 3;
}
```

- Request 和 Response 共用 `cmd_id` 和 `request_id`。
- Notify 使用独立 `cmd_id`，`request_id` 必须为 0。
- Notify 仍使用统一的 `Message`，不定义另一套包体。

## 6. Handler 命名和签名

Handler 使用请求方向命名：

```text
<来源>2<目标><业务动作>Handler
```

Handler 的存放位置由名称中的“目标”和生成器的 Handler 导入路径模板共同决定。默认模板是 `forge/services/{service}/handler`：

| 目标 | Handler 包 |
| --- | --- |
| `G` | `services/gateway/handler` |
| `L` | `services/logic/handler` |
| `S` | `services/scene/handler` |
| `C` | 不生成服务端 Handler，由 Gateway 的回包/转发链路送往客户端 |

因此在默认配置下，`C2LLoginHandler` 位于 `services/logic/handler`，建议文件为 `services/logic/handler/login.go`。`{service}` 会根据目标展开为 `gateway`、`logic` 或 `scene`。如果目录不叫 `handler`，可以改为：

```bash
./scripts/gen.sh \
  -handler-import-pattern 'forge/services/{service}/handlers'
```

此时 `C2LLoginHandler` 的导入路径为 `forge/services/logic/handlers`。模板必须包含 `{service}`，不在生成器中硬编码最后一级目录名。生成代码会把该包统一别名为 `handler`，因此导入路径的最后一段不必叫 `handler`。

请求响应 Handler：

```go
package handler

func C2LLoginHandler(
	req *c2l.C2LLoginRequest,
	rsp *c2l.L2CLoginResponse,
) error {
	return nil
}
```

单向通知 Handler：

```go
package handler

func G2LSessionClosedHandler(
	notify *g2l.G2LSessionClosedNotify,
) error {
	return nil
}
```

当前 Handler 按项目约定只接收具体业务结构。网络连接、统一消息头和 Protobuf 编解码由生成的 Command 处理。

生成器不扫描 Handler 来发现协议，而是根据生成后的 Protobuf 消息名和 `CommandID` 自动生成对固定 Handler 名的调用。Handler 尚未实现时生成仍可完成，但 Go 编译会提示缺失的 Handler 包或函数。

依赖方向固定为：

```text
services/logic 组装层 -> command/c2l -> <配置后的 Logic Handler 包>
```

配置后的 Logic Handler 包不得反向导入 `command/c2l` 或 `services/logic` 根包，否则会形成 Go 循环依赖。Handler 所需的领域能力应下沉到 Logic 服务内独立的子包。

## 7. 生成的代码

请求响应 Handler 生成：

```text
C2LLoginCommand.Execute
  -> 校验 MessageKind_REQUEST
  -> Payload 解码为 C2LLoginRequest
  -> 创建 L2CLoginResponse
  -> 调用 handler.C2LLoginHandler(req, rsp)
  -> 编码 L2CLoginResponse
  -> 使用相同 cmd_id/request_id 发送 RESPONSE
```

通知 Handler 生成：

```text
G2LSessionClosedCommand.Execute
  -> 校验 MessageKind_NOTIFY
  -> Payload 解码为 G2LSessionClosedNotify
  -> 调用 handler.G2LSessionClosedHandler(notify)
```

生成器按照业务 Proto 文件分组，并将 Proto 目录结构镜像到 `-out-dir` 指定的 Command 根目录：

```text
api/proto/item.proto       -> command/command_gen_item.go
api/proto/c2l/item.proto   -> command/c2l/command_gen_item.go
api/proto/c2s/scene.proto  -> command/c2s/command_gen_scene.go
```

生成器递归扫描 `api/pb`，优先读取 protoc 生成文件头中的 `// source: c2l/item.proto` 确定原始 Proto 路径，不依赖 `.pb.go` 的存放层级。

每个 Command 子目录是一个独立 Go 包。生成的 Command 与手写 Handler 分属不同包：

```text
api/proto/c2l/item.proto
command/c2l/command_gen_item.go
command/c2l/command_gen_register.go
services/logic/handler/item.go
```

业务协议按方向生成到独立 Go 子包：

```proto
// api/proto/c2l/item.proto
option go_package = "forge/api/pb/c2l";
```

生成的胶水代码会分别导入统一包头 `forge/api/pb`、业务 Payload 包 `forge/api/pb/c2l` 和根据模板展开的目标服务 Handler 包。不同方向目录可以存在同名 Proto 文件，因为生成目标包彼此独立。

一组 Request 和 Response 必须定义在同一个 Proto 文件中：

```proto
// c2l/item.proto
message C2LUseItemRequest {}
message L2CUseItemResponse {}
```

不允许将它们拆分到不同文件。这样一个业务协议文件与一份胶水代码保持对应，删除或移动协议时不会留下无法确认归属的 Command。

每个文件生成自己的内部注册函数：

```go
// command/c2l/command_gen_item.go
func registerItemCommands(router *session.Router) {
	router.Register(pb.CommandID_COMMAND_ID_C2L_USE_ITEM, C2LUseItemCommand{})
}
```

每个 Command 子包分别生成自己的 `command_gen_register.go`：

```go
// command/c2l/command_gen_register.go
package c2l

func RegisterCommands(router *session.Router) {
	registerItemCommands(router)
	registerPlayerCommands(router)
}
```

不同服务只导入并注册自己需要的方向包：

```go
import (
	"forge/command/c2l"
	"forge/command/g2l"
	"forge/command/s2l"
)

func registerLogicCommands(router *session.Router) {
	c2l.RegisterCommands(router)
	g2l.RegisterCommands(router)
	s2l.RegisterCommands(router)
}
```

生成器不生成跨目录的总注册函数，避免一个服务意外注册其他服务的协议。

## 8. 生成器校验

生成前必须检查：

1. 消息名符合 `<来源>2<目标><动作><Request|Response|Notify>`。
2. 来源和目标属于 `C/G/L/S` 且不能相同。
3. Request 必须存在方向反转、业务动作相同的 Response。
4. Request 和 Response 必须位于同一个 Proto 文件。
5. Response 必须存在唯一匹配的 Request。
6. Request 或 Notify 必须存在对应的 `COMMAND_ID_<方向>_<动作>`。
7. 消息方向必须与 Proto 的一级目录一致，例如 `C2L*` 只能定义在 `api/proto/c2l` 下。
8. Proto 源码目录、生成的 Go Payload 子包和 Command 输出目录保持一致。
9. 目标为 `C` 的 Response/Notify 不生成服务端 Handler 胶水。
10. 生成结果顺序固定，避免无意义的文件变化。

生成代码不覆盖业务 Handler 文件。

## 9. 使用方式

在项目根目录执行：

```bash
make gen
```

Makefile 只调用 `scripts/gen.sh`。该脚本先调用 `scripts/proto.sh` 递归编译 `api/proto` 下的全部协议，再执行 Command 生成器。脚本显式传入项目默认值 `-handler-import-pattern 'forge/services/{service}/handler'`；脚本收到的命令行参数会追加在默认参数之后，同名参数以后一个为准。生成器的 `-out-dir` 是必填参数，`-pb-import` 默认为 `forge/api/pb`，`-handler-import-pattern` 默认为 `forge/services/{service}/handler`。

只生成 Protobuf Go 代码时执行：

```bash
make proto
```

输出文件遵循：

```text
item.proto       -> command_gen_item.go
c2l/item.proto   -> c2l/command_gen_item.go
c2s/scene.proto  -> c2s/command_gen_scene.go
目录注册入口     -> 各目录的 command_gen_register.go
```

生成文件头包含：

```go
// Code generated by forge-command-gen. DO NOT EDIT.
```

生成模板位于：

```text
cmd/gencmd/command.tpl
```

模板通过 `go:embed` 编译进生成器，执行 `go run ./cmd/gencmd` 时不依赖当前工作目录，也不需要额外复制模板文件。`command.tpl` 中分别定义 `command` 和 `registry` 两个模板，负责单个 Proto 文件的胶水代码和目录级注册入口。

再次生成时，工具只会清理带有上述生成标记、且已不再对应任何 Proto 文件的旧 `command_gen_*.go`。不会删除同名模式下的手写文件。

