# Pape-MUIP

Pape-MUIP 是使用 Gin 构建的 Pape-SDK 与 Pape-BOOI 管理员 Web 控制台。MUIP 不打开 SDK/BOOI
业务数据库：账号操作全部经 SDK Inner API，玩家操作全部经相应 BOOI Inner API。
MUIP 唯一直接读取的数据库是本目录的 `languages.sqlite`，且以 SQLite
`mode=ro&immutable=1` 打开，用于把资源配置中的文本 ID 解析成本地化名称。

## 功能

- SDK 账号查询、创建、修改手机号、修改密码、登出全部设备、删除；
- BOOI 角色查询、创建、资料修改、封禁/解封、协议下线、解绑删除；
- 在玩家页上传解码后的 `SyncUserTotalDataReply` 抓包，由 MUIP 解析成标准化行数据和普通 ID 列表后导入；
- 由 MUIP 遍历当前 BOOI 资源目录并生成普通发放列表，将账号补齐为当前资源版本的完整账号；
- 资产、思念、皮肤、任务、关卡、引导、系统解锁、男主羁绊和主线进度管理；
- 从 BOOI 资源目录检索思念、皮肤、物品等，并通过 `languages.sqlite` 显示当前语言的本地化名称；
- 邮件中心：选择多个玩家，使用 `SystemMail` 资源模板或撰写自定义邮件，设置附件、有效期和自动领取后批量投放；在线玩家会收到原生邮件更新推送；
- 界面语言与资源集根据浏览器语言自动选择，也可以在右上角切换 `languages.sqlite` 中有文本数据的资源集；资源目录名称和邮件模板文本使用同一资源集解析。数据库会保留尚未展开的资源集来源记录，但不会把它们显示为可选语言；
- 使用独立 HTML 登录页和 HttpOnly/SameSite 会话 Cookie，界面自动跟随系统深浅色；
- BOOI GM 写入成功后尽可能向在线客户端发送协议原生 UpdateReply，API 返回
  `notified` 表示实际通知的在线会话数。没有独立增量协议的状态由下次 Sync 恢复。
- 玩家“断开连接”会先发送协议原生 `BeKickedReply`，再撤销该账号的临时游戏令牌；
  服务端不直接关闭 TCP，由客户端按正常下线流程退出。

## 构建与运行

1. 确保 SDK `inner_api` 与 BOOI `inner` 已启用。
2. 复制 `config.example.yaml` 为 `config.yaml`，填写两侧 Inner Token 和 MUIP 管理密码。
3. 从命令入口构建：

```powershell
go test ./...
go build -o pape-muip.next.exe .\cmd\pape-muip
```

Paper 工作区的生产实例由 WinSW 服务 `MumlPapeMUIP` 管理。替换
`pape-muip.exe` 后必须通过 Windows 服务重启，不要直接运行 EXE 代替服务。

## 项目结构

```text
cmd/pape-muip/       可执行程序入口，仅处理命令行参数和启动错误
internal/app/        Gin 路由、登录会话、Inner API 代理和管理业务
internal/app/web/    编译进程序的管理后台静态资源
internal/config/     配置模型、默认值、校验和相对路径解析
```

浏览器访问 `http://127.0.0.1:65286`，在登录页输入 `admin_user` / `admin_password`。
默认仅监听回环地址；跨主机部署应放在受信网络后并使用 HTTPS/mTLS。

## Inner API 约定

MUIP 使用以下新增管理前缀：

- SDK：`/inner/v1/admin/accounts`
- BOOI：`/inner/v1/admin/players`、`/inner/v1/admin/catalog`、`/inner/v1/admin/mail/templates`、`/inner/v1/admin/mail/send`；批量写入使用
  `/players/:id/state` 和 `/players/:id/grants`，BOOI 不提供 complete 策略接口。

这些接口与现有 Inner API 共用 Bearer Token，未挂载到公共 SDK/游戏 HTTP 路由。
