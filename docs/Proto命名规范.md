# Forge Proto 命名规范

适用范围：Forge 项目的 `.proto` 文件。制定日期：2026 年 10 月 26 日。

基本规则：**类型使用大驼峰，字段使用小写下划线，枚举值使用大写下划线。** 本规范以 [Protobuf 官方 Style Guide](https://protobuf.dev/programming-guides/style/) 为依据；下面明确标注的项目约定用于统一 Forge 内部写法。

## 命名规则

| 对象 | 格式 | 示例 |
| --- | --- | --- |
| 文件名 | `lower_snake_case.proto` | `gateway_message.proto` |
| package | 小写，使用点分层 | `forge.protocol.v1` |
| message | 大驼峰 `TitleCase` | `MessageHeader` |
| 字段 | 小写下划线 `lower_snake_case` | `request_id` |
| oneof | 小写下划线 | `message_body` |
| enum 类型 | 大驼峰 | `MessageKind` |
| enum 值 | 大写下划线，并带枚举类型前缀 | `MESSAGE_KIND_REQUEST` |
| service | 大驼峰 | `GatewayService` |
| rpc 方法 | 大驼峰 | `GetSession` |

不要在 proto 字段中使用 `requestId` 或 `RequestID`。重复字段使用复数，例如 `repeated uint64 user_ids`。

缩写按一个普通单词处理：类型使用 `GetUidRequest`、`HttpConfig`，字段使用 `user_id`、`http_config`，不将缩写拆成 `u_i_d`。标识符不以下划线开头或结尾，不使用连续下划线；下划线后接字母，版本写 `protocol_v2`，不写 `protocol_2`。

枚举首项使用数值 0，命名为带类型前缀的 `UNSPECIFIED` 或 `UNKNOWN`。Forge 统一优先使用 `UNSPECIFIED`，表示未指定，不赋予它有效业务操作含义。每个枚举值都保留类型前缀，避免同一 package 内发生名称冲突。

## Forge 项目约定

- 设计文档使用中文文件名；`.proto` 和程序源码使用英文命名。
- Proto package 采用 `forge.<模块>.v1` 形式，例如 `forge.protocol.v1`；版本表示协议命名空间，不随每次发布递增。
- 每个 Go 生成目标显式设置 `go_package`，与当前 module `forge` 及生成目录一致。
- 成对业务消息采用 `XxxRequest`、`XxxResponse`，避免同一模块混用 `Req`、`Request`、`Rsp`、`Result` 表达同一种角色。
- 消息名称描述用途，不使用无信息量的 `Data1`、`Info2`。字段名称表达语义；带单位的值应在名称或注释中明确单位。
- 生成代码的标识符交给生成器处理，不为了手写 Go 的缩写风格修改生成文件。

这些约定不分配业务 cmd，也不决定是否保留 NOTIFY 类型；业务结构仍需单独设计。

## 命名示例

以下只展示命名，不作为业务协议定义。

```proto
syntax = "proto3";

package forge.protocol.v1;

option go_package = "forge/api/gen/go/forge/protocol/v1;protocolv1";

enum MessageKind {
  MESSAGE_KIND_UNSPECIFIED = 0;
  MESSAGE_KIND_REQUEST = 1;
  MESSAGE_KIND_RESPONSE = 2;
}

message MessageHeader {
  uint32 cmd = 1;
  uint64 request_id = 2;
  MessageKind kind = 3;
}
```

## 格式与评审

使用两个空格缩进，字符串使用双引号。文件按说明、syntax/edition、package、imports、options、类型与服务定义组织；imports 排序。

提交时检查文件名、类型名、字段和枚举值是否符合上表，package 与 go_package 是否正确，重复字段是否使用复数，是否误改生成代码。

对已发布协议进行命名调整前，应检查生成 API、JSON 字段及调用方兼容性，不能仅因风格不统一直接批量改名。新协议从开始就遵循本规范。
