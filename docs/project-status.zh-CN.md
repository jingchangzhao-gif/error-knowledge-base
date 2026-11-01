# 项目状态

最后核对：2026-10-25，对应 tag `v0.1.3`。

## 当前发布

`v0.1.3` 是受支持的公开版本。发布形态是便携工具箱：`errkb-toolbox.zip`（同一发布页另附独立 `errkb.exe`）。没有安装器，也没有开始菜单入口；把 ZIP 解压到你有写权限的目录，运行一次 `errkb.exe pg install` 即可。

## 已发布内容

| 领域              | 状态                                                                                                                                                                                                                                                           |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 可复用核心        | 规范化、指纹、标题提取、分类、分词、Jaccard、重排、统一脱敏，全部有 Go 测试                                                                                                                                                                                    |
| PostgreSQL schema | 迁移 0001–0008；幂等记录、可信度、精确与 trigram 检索、解法与结果、保留、备份/恢复                                                                                                                                                                             |
| CLI               | `db`、`run`、`tail`、`markdown`、`search`、`list`、`show`、`stats`、`record`、`feedback`、`mcp`、`ui`、`pg`、`doctor`、`version`                                                                                                                               |
| 后台服务与 API    | 命名管道 JSON API，带 schema 版本的信封；采集门禁遵守 `capture.enabled`、暂停的连接器与项目；数据库断开时只写已脱敏事件并在恢复后重放                                                                                                                          |
| MCP 服务          | 九个工具（`err_lookup`、`err_search`、`err_record`、`err_record_solution`、`err_set_status`、`err_get_issue`、`err_feedback`、`err_list`、`err_stats`）；自行解析安装凭据并在需要时启动后台服务；`errkb mcp install --host …` 可直接注册到 Codex / Claude Code |
| 宿主              | DSH 适配器（request-error 瀑布、providers、去重、回传）、Codex 包装器、Claude Code 包装器；实测记录见 `docs/connector-matrix.zh-CN.md`                                                                                                                         |
| Web 工具箱        | 仅回环、token + CSP：搜索、问题详情与带审计的编辑、统计/审计、设置（连接器、采集、保留、带预览的删除）                                                                                                                                                         |
| 发布物            | `errkb-toolbox.zip`（errkb.exe、启动器、登录脚本、PostgreSQL 封装、迁移、文档）、`SHA256SUMS`、manifest、SBOM、第三方声明、签名策略；配置 `NPM_TOKEN` 时同一 tag 还会发布 npm 包                                                                               |
| CI                | `ci`、`go`、`cue`、`database`、`windows-filesystem`、`windows-go`、`compat`；发布工作流构建 ZIP 并发布 npm 包                                                                                                                                                  |

## Unreleased 源码：预防闭环

当前源码包含**已实现并经本地验证、但尚未发布**的预防闭环；它不属于 v0.1.3：

| 领域          | 已实现并经本地验证的行为                                                                    |
| ------------- | ------------------------------------------------------------------------------------------- |
| 契约与 schema | Prevention v1 契约、0009 迁移、不可变版本、issue 关联、验证历史与激活状态                   |
| CLI 与服务    | 六个 `prevention` 命令、真实有界验证、干净且已跟踪的 Git 激活证据、稳定 list/get/stats 读取 |
| MCP 与 Web    | MCP 共十二工具；预防 register/list/get 不提供可执行状态迁移；Web 预防统计只读               |
| 指标          | 验证比例、按 issue/版本的 30 天窗口、observing/mature 状态、全时间复发与可空成熟比率        |
| 运维          | 四张预防表进入备份/恢复和项目删除计数；连接器/时间删除与保留策略保留 active 产物            |
| 升级证据      | 真实 PostgreSQL 0007→0009 直接升级、0010 失败回滚和未来账本拒绝测试均已在本地通过           |

完整 PR/CI 兼容分类和 19 条规则自检仍是发布/CI 门禁；本地测试不宣称新版本已经通过。详见 [`prevention.zh-CN.md`](prevention.zh-CN.md)。

## 已验证

- `go test ./...`（真实 PostgreSQL）、CUE 契约与兼容检查器（19 条规则）在 CI 中运行；Windows 冒烟任务运行同一个可执行文件。
- 发布：tag `v0.1.3`、变更日志小节、二进制 version/commit/build time、逐文件 `SHA256SUMS` 与 npm tarball 均由 `packaging/windows/release.ps1` 检查。
- 连接器行为带版本与日期记录在 [`connector-matrix.zh-CN.md`](connector-matrix.zh-CN.md)；未实测格子会明确标注。

## 已知缺口

- 物理重启与交互视觉检查没有自动化；登录后的行为以确定性方式验证（后台服务/PostgreSQL 重启并重放 Run 键命令）。
- PostgreSQL 16 是前置条件。数据库不内嵌；`errkb pg install` 管理专用实例，但仍需已安装的 PostgreSQL 二进制。
- 发布物未做代码签名；完整性依赖 `SHA256SUMS`。
- DSH STORE 目录重新上架尚未完成（上一次提交仍在审核）。
- 部分宿主能力仅限 Windows（管道、托盘、Win32 窗口）。

## 获取方式

从[最新发布页](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/latest)下载 `errkb-toolbox.zip`（或独立 `errkb.exe`）。安装、升级与卸载步骤见 [`install.zh-CN.md`](install.zh-CN.md)；安装形态就是该页描述的便携布局，不是安装器。
