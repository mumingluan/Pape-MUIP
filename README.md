# Pape-MUIP

Pape-MUIP 是 Pape-SDK 与 Pape-BOOI 的管理员 Web 控制台。MUIP 不打开 SDK/BOOI
业务数据库：账号操作全部经 SDK Inner API，玩家操作全部经相应 BOOI Inner API。
MUIP 唯一直接读取的数据库是本目录的 `languages.sqlite`，且以 SQLite
`mode=ro&immutable=1` 打开，用于把资源配置中的文本 ID 解析成本地化名称。

## 功能

- SDK 账号查询、创建、修改手机号/密码、撤销令牌、签发会话、删除；
- BOOI 角色查询、创建、资料修改、封禁/解封、解绑删除；
- 资产、思念、皮肤、任务、关卡、引导、系统解锁、男主羁绊和主线进度管理；
- 从 BOOI 资源目录检索思念、皮肤、物品等，并通过 `languages.sqlite` 显示中文名；
- BOOI GM 写入成功后尽可能向在线客户端发送协议原生 UpdateReply，API 返回
  `notified` 表示实际通知的在线会话数。没有独立增量协议的状态由下次 Sync 恢复。

## 启动

1. 确保 SDK `inner_api` 与 BOOI `inner` 已启用。
2. 复制 `config.example.yaml` 为 `config.yaml`，填写两侧 Inner Token 和 MUIP 管理密码。
3. 启动：

```powershell
go run . -config .\config.yaml
```

或构建后运行：

```powershell
go build -o pape-muip.exe .
.\pape-muip.exe -config .\config.yaml
```

浏览器访问 `http://127.0.0.1:18080`，使用 `admin_user` / `admin_password` 的 HTTP
Basic Auth 登录。默认仅监听回环地址；跨主机部署应放在受信网络后并使用 HTTPS/mTLS。

## Inner API 约定

MUIP 使用以下新增管理前缀：

- SDK：`/inner/v1/admin/accounts`
- BOOI：`/inner/v1/admin/players`、`/inner/v1/admin/catalog`

这些接口与现有 Inner API 共用 Bearer Token，未挂载到公共 SDK/游戏 HTTP 路由。
