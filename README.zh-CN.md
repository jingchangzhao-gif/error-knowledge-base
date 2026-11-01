# 错误知识库（Error Knowledge Base）

[English](README.md) · [许可证](LICENSE)

> **同一个错误只诊断一次：记录真实解法，下次失败时直接复用——全程本地完成，不调用模型。**

错误知识库（`errkb`）是面向命令行工作流和 AI 编程工具的本地报错复用与预防平台。它采集可观察的失败，把脱敏、去重后的知识保存到 PostgreSQL，在错误再次出现时返回带证据和可信度的已知解法，并跟踪仓库中的预防产物。

## 发布版与源码状态

| 版本              | 包含内容                                                                                                       |
| ----------------- | -------------------------------------------------------------------------------------------------------------- |
| **已发布 v0.1.3** | 错误采集、检索、版本化解法、结果与可信度；CLI、后台服务、Web 工具箱和连接器；迁移 `0001`–`0008`；9 个 MCP 工具 |
| **当前 `main`**   | 包含以上能力，并新增预防闭环、复发指标、迁移 `0009` 和 12 个 MCP 工具；已通过测试但尚未发布                    |

从 [GitHub Releases](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/latest) 下载便携工具箱。仅存在于源码中的能力会标记为 **Unreleased**，避免把开发分支与下载发布版混为一谈。

## 目前能做什么

| 目标                       | 功能                                                                                 |
| -------------------------- | ------------------------------------------------------------------------------------ |
| 采集失败                   | 包装 CLI 命令、观察受支持宿主的 Hook、跟随日志或提交 `ErrorEvent v1` 载荷            |
| 保护本地数据               | 写库前脱敏；默认使用本地 Named Pipe，没有遥测，也不调用外部模型                      |
| 识别重复错误               | 按项目分类、规范化、计算指纹并去重；重复事件以幂等方式追加 occurrence                |
| 查找已知解法               | 先检索精确标识，再返回最多三个明确标记的近似候选                                     |
| 带上下文复用               | 返回已保存解法、来源、匹配原因、环境警告和实测可信度                                 |
| 从结果中学习               | 保留解法版本，并记录特定版本的成功、失败或未知结果                                   |
| 预防复发（**Unreleased**） | 版本化回归测试、确定性检查、代理指令和意图；验证、激活并测量真实复发                 |
| 本地运维                   | 使用 Web 工具箱、MCP、诊断、迁移、备份恢复、保留策略、审计删除和便携 PostgreSQL 管理 |

每项功能的准确实现与验证边界见 [`docs/features-and-implementation.zh-CN.md`](docs/features-and-implementation.zh-CN.md)。

## 工作流程

```text
CLI / 宿主 Hook / 日志 / 手工事件
                │
                ▼
       脱敏 → 分类 → 规范化 → 指纹
                │
                ▼
        本地后台服务 → PostgreSQL
                │
                ▼
 精确匹配 → 有界近似候选 → 解法 + 原因 + 可信度
```

- CLI 包装器只采集失败，成功命令不会入库。
- 只有结构化字段不矛盾的精确指纹匹配才可复用；近似匹配始终需要人工确认。
- 解法来自已保存的解法版本，不是模型临时生成的新答案。
- 可信度根据真实结果计算，未知结果不会改变分数。

## 快速开始

### 环境要求

- Windows 10 或 11，x64。
- PostgreSQL 16。errkb 管理专用本地实例，数据库不内嵌。
- 运行发布版不需要 Go、Node.js 或其他开发工具链。

### 安装并打开

把 `errkb-toolbox.zip` 解压到有写权限的目录，然后运行：

```powershell
.\errkb.exe pg install
.\start-errkb.cmd
```

第一条命令创建本地实例、应用迁移并写入凭据；第二条打开仅监听回环地址的 Web 工具箱，并在需要时启动后台服务。若要复用已有 PostgreSQL，改用 `errkb db migrate --dsn <你的 DSN>`。

自定义端口、升级、登录恢复和卸载见 [`docs/install.zh-CN.md`](docs/install.zh-CN.md)。

### 完成第一次报错复用

用 errkb 运行一个真实项目命令：

```powershell
.\errkb.exe run --connector cli --session local --project my-repo -- npm test
```

如果失败，在 Web 工具箱中打开该问题并记录已验证解法，或者使用 MCP `err_record_solution`。相同失败再次出现时，重新运行该命令即可取回解法。

记录这个解法版本是否有效：

```powershell
.\errkb.exe feedback <issue-id> <solution-version-id> --outcome success
.\errkb.exe search "module not found"
.\errkb.exe show <issue-id>
.\errkb.exe stats
```

通用 CLI 是在**观察到失败之后**返回已知解法。受支持的 DSH 和 Claude Hook 还可以在新诊断前提供常驻提示或通知。

## 接入 AI 宿主

| 宿主        | 已实现的接入方式                                           |
| ----------- | ---------------------------------------------------------- |
| 通用 CLI    | 通过 `errkb run -- <command>` 采集失败并查询已知解法       |
| DSH         | 插件 Hook、常驻提示和执行后通知                            |
| Codex CLI   | CLI 包装器和 MCP 工具；不宣称具有失败前 Hook               |
| Claude Code | `PostToolUseFailure` Hook、CLI 包装器、会话提示和 MCP 工具 |

向受支持宿主注册 MCP：

```powershell
.\errkb.exe mcp install --host codex
.\errkb.exe mcp install --host claude
.\errkb.exe mcp install --host dsh
.\errkb.exe mcp selftest
```

当前 `main` 提供 12 个 MCP 工具，已发布的 v0.1.3 提供 9 个。MCP 允许模型或用户主动查询及更新获准的知识；只安装 MCP 不会被动监视宿主。实测版本与未支持路径见 [`docs/connector-matrix.zh-CN.md`](docs/connector-matrix.zh-CN.md)。

## 当前 `main` 的预防闭环

把一个已解决问题转化为经过审查、可版本化的预防产物：

1. 登记仓库相对路径及其 SHA-256，创建草稿。
2. 通过 CLI 显式运行一个有界验证命令。
3. 由你提交产物，再在干净 Git `HEAD` 上激活匹配的已验证版本。
4. 根据真实 occurrence 和激活前后 30 天窗口观察复发。

```powershell
errkb prevention add --issue <已解决-issue-id> --kind regression_test --title "Rename regression" --repo . --path tests/rename_test.go
errkb prevention verify <artifact-version-id> --repo . -- go test ./...
# 激活前由你审查并提交产物。
errkb prevention activate <artifact-version-id> --repo .
errkb prevention stats --issue <已解决-issue-id>
```

四种产物类型为 `regression_test`、`deterministic_check`、`agent_instruction` 和 `intent`。MCP 可以登记元数据和读取预防记录，但只有人工调用的 CLI 能验证或激活；Web 预防统计只读。

激活只记录证据，**不会**安装 CI 任务或宿主 Hook、编辑文件或执行 Git 写操作。状态规则、指标、隐私和故障排查见 [`docs/prevention.zh-CN.md`](docs/prevention.zh-CN.md)。

## 命令总览

| 命令                                                        | 用途                                 |
| ----------------------------------------------------------- | ------------------------------------ |
| `errkb run -- <command>`                                    | 运行命令、保持原行为并记录失败       |
| `errkb search <query>` / `show <id>`                        | 查找并查看已知问题与解法             |
| `errkb record --event <file>`                               | 从文件或 stdin 提交完整事件          |
| `errkb list` / `stats`                                      | 列出问题或解法并读取汇总指标         |
| `errkb feedback <issue> <version>`                          | 记录解法结果                         |
| `errkb prevention add\|verify\|activate\|list\|show\|stats` | 管理并观察预防产物（**Unreleased**） |
| `errkb tail --file <path>`                                  | 从持续增长的日志采集失败             |
| `errkb ui --web`                                            | 打开本地 Web 工具箱                  |
| `errkb mcp` / `mcp install` / `mcp selftest`                | 提供、注册并验证 MCP                 |
| `errkb db migrate\|status`                                  | 应用或检查 schema 迁移               |
| `errkb pg install\|start\|stop\|status\|uninstall`          | 管理本地 PostgreSQL 实例             |
| `errkb doctor`                                              | 执行只读环境、连接器和 MCP 诊断      |
| `errkb markdown import\|export`                             | 交换旧版 `ERRORS.md` 格式            |

多数读写命令支持 `--json`，返回带版本的契约载荷。

## 隐私与运行边界

- 知识保存在本机。核心不调用模型、不发起外网请求，也不上报遥测。
- 写库前完成脱敏。HTTP 默认关闭；MCP 使用 stdio，本地 API 使用仅所有者访问的 Named Pipe。
- 预防存储只包含元数据、哈希和有界脱敏证据，不包含产物正文或仓库绝对路径。
- 保留函数和设置已经存在，但后台服务尚未接入自动定时清理。
- DSH 适配器离线队列可能在核心脱敏前，暂时把原始观测保存在本机磁盘。

存储敏感项目数据前请阅读 [`docs/privacy.zh-CN.md`](docs/privacy.zh-CN.md)。

## 当前限制

- 必须安装 PostgreSQL 16；errkb 不是内嵌数据库的单文件应用。
- 发布物未做代码签名，请核对发布页的 `SHA256SUMS`。
- 预防产物激活不会自动把它接入 CI 或宿主工作流。
- errkb 不生成回归测试、不编辑预防文件、不自动写入 Git，也不自动执行检索到的修复。
- 不提供团队/云同步、向量检索、私有思维链采集或可靠的幻觉检测。
- 通用日志/手工宿主验收，以及连接器矩阵中明确标为未验证的路径仍未完成。

## 文档

| 文档                                                                                                                                      | 内容                       |
| ----------------------------------------------------------------------------------------------------------------------------------------- | -------------------------- |
| [`docs/project-status.zh-CN.md`](docs/project-status.zh-CN.md) / [English](docs/project-status.md)                                        | 发布、源码和验证状态       |
| [`docs/install.zh-CN.md`](docs/install.zh-CN.md) / [English](docs/install.md)                                                             | 安装、升级、恢复和卸载     |
| [`docs/features-and-implementation.zh-CN.md`](docs/features-and-implementation.zh-CN.md) / [English](docs/features-and-implementation.md) | 功能到源码的实现映射       |
| [`docs/prevention.zh-CN.md`](docs/prevention.zh-CN.md) / [English](docs/prevention.md)                                                    | 预防工作流与指标           |
| [`docs/connector-matrix.zh-CN.md`](docs/connector-matrix.zh-CN.md) / [English](docs/connector-matrix.md)                                  | 已实测宿主能力与缺口       |
| [`docs/privacy.zh-CN.md`](docs/privacy.zh-CN.md) / [English](docs/privacy.md)                                                             | 数据存储、脱敏、保留和删除 |
| [`docs/benchmarks.zh-CN.md`](docs/benchmarks.zh-CN.md) / [English](docs/benchmarks.md)                                                    | 性能证据与测试环境         |
| [`CHANGELOG.md`](CHANGELOG.md)                                                                                                            | 发布历史与 Unreleased 变化 |

历史设计计划和验收记录仍保留在 `docs/` 中，它们是证据，不再作为当前用户入口重复展开。

## 开发

```powershell
pnpm install
pnpm typecheck
pnpm lint
pnpm format:check
pnpm test
pnpm build

go build ./...
go vet ./...
gofmt -l .
cue vet ./spec/...
```

数据库集成测试需要 PostgreSQL 和 `ERRKB_TEST_DATABASE_URL`；未设置时会跳过。Windows 打包脚本位于 [`packaging/windows/`](packaging/windows)。

贡献代码时请保持双语文档同步，并遵守现有契约、隐私和验证门禁。仓库约定见 [`docs/implementation-plan.zh-CN.md`](docs/implementation-plan.zh-CN.md)。

## 许可证

[MIT](LICENSE) © jingchangzhao-gif
