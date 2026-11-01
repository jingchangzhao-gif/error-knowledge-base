# 报错预防闭环

[English](prevention.md)

> **源码状态：Unreleased。** 预防闭环已在当前源码中实现并测试，但不属于已发布的 v0.1.3 工具箱。

预防闭环把一个已解决的错误转化为可版本化、可审查的预防产物，用真实命令验证该产物，把激活绑定到干净的 Git 提交，再观察后续复发。它扩展现有的“采集 → 检索 → 结果”闭环，不调用模型，也不修改仓库文件。

预防元数据描述的是意图和证据；激活**不会**自动在每次构建中运行检查、向宿主注入指令，或执行检索到的修复。需要由你显式把产物接入自己的测试、CI 或宿主工作流。

## 四种产物

| 类型                  | 预期产物                                 | 典型验证                               |
| --------------------- | ---------------------------------------- | -------------------------------------- |
| `regression_test`     | 能复现原始失败、现在应当通过的测试       | 运行对应测试或所在包测试套件           |
| `deterministic_check` | Lint 规则、脚本、schema 检查等确定性守卫 | 运行检查并以退出码判断结果             |
| `agent_instruction`   | 仓库内经审查的指令或检查清单             | 运行确定性校验器；仅激活不会自动注入   |
| `intent`              | 经审查、必须持续成立的行为意图           | 关联显式验证命令；不能仅凭文字视作证明 |

每个产物属于一个项目，有标题、仓库相对路径，以及同一项目内的一个或多个关联 issue。内容哈希变化会追加一个不可变版本。产物正文留在仓库；PostgreSQL 只保存元数据、哈希、状态迁移、验证结果和审计记录。

## 状态与决策权限

```text
已解决 issue + 本地文件
        │ prevention add
        ▼
      draft ── 验证通过 ──▶ verified
        │                    │
        │              最新验证通过 + 产物干净、
        │              已跟踪于 Git HEAD
        │                    │
        └────── 失败验证会留痕，但不会激活
                             ▼
                           active
                             │
                    激活替代的新版本
                             ▼
                         deprecated
```

失败验证也是有价值的证据。只有最新验证针对同一哈希通过，才允许激活。新 occurrence 不会自动弃用版本，也不会改变 issue 状态。

只有人工调用的 CLI 能执行验证或激活版本。MCP 可以登记元数据并读取记录；Web 统计区只读。核心不调用模型，不自动生成测试或修改文件，也不执行 Git 写操作。

## 前置条件

- 使用 Unreleased 源码构建，PostgreSQL 已迁移到 `0009`，daemon 可用。
- 先准备一个**已解决**的 issue ID。
- 产物使用仓库相对的斜杠路径，例如 `tests/rename_test.go`。绝对路径、反斜杠、`.`/`..`、`.git`、符号链接和非普通文件都会被拒绝。
- 文件不得超过 **1 MiB**。
- 激活需要 Git、有效 `HEAD`，产物已被该提交跟踪且没有未提交修改。errkb 使用的 Git 命令全部只读。

## 六个 CLI 命令

| 命令                        | 用途                                     |
| --------------------------- | ---------------------------------------- |
| `errkb prevention add`      | 哈希本地产物，登记草稿/版本或 issue 关联 |
| `errkb prevention verify`   | 执行一个有界命令并追加真实结果           |
| `errkb prevention activate` | 复核哈希与 Git 状态，然后激活已验证版本  |
| `errkb prevention list`     | 按 issue、项目、类型或状态分页/过滤产物  |
| `errkb prevention show`     | 读取产物、全部版本和验证历史             |
| `errkb prevention stats`    | 读取实际观察到的复发与验证指标           |

所有命令都支持 CLI 全局 `--json`。`verify` 在失败结果入库后仍返回非零 CLI 状态，避免自动化把“失败已记录”误当作“验证成功”。

## 完整示例

假设 issue 已经解决，且当前仓库中的 `tests/rename_test.go` 是真实回归测试。

```powershell
# 1. 登记当前字节；不会把产物正文发送到 PostgreSQL。
errkb prevention add --issue <已解决-issue-id> --kind regression_test --title "Rename regression" --repo . --path tests/rename_test.go

# 2. 使用 add 返回的 artifact_version_id。命令在 --repo 中运行。
errkb prevention verify <artifact-version-id> --repo . -- go test ./...

# 3. 由你提交刚刚测试过的产物。激活只读取 Git 状态。
git add tests/rename_test.go
git commit -m "Add rename regression test"
errkb prevention activate <artifact-version-id> --repo .

# 4. 查看状态和证据。
errkb prevention list --project <项目键> --status active
errkb prevention show <artifact-id>
errkb prevention stats --project <项目键>
errkb prevention stats --issue <已解决-issue-id> --json
```

需要修订产物时，修改文件后用同一 issue、类型和路径再次运行 `prevention add`。SHA-256 变化会创建新草稿。必须显式验证并激活该版本；之前的 active 版本会变为 deprecated，同时保留历史。

## MCP 边界

Unreleased MCP 服务共有十二个工具：原有九个，加上：

- `err_prevention_register`：接收元数据和调用方提供的 SHA-256。它**不会**读取文件，也**不会**证明哈希与仓库内容一致。
- `err_prevention_list`：读取过滤后的产物/版本分页结果。
- `err_prevention_get`：读取一个产物、其版本和验证历史。

MCP 刻意不提供 verify 或 activate 工具；这些决策必须使用 CLI。Web 工具箱只提供只读 Prevention 统计区，没有预防写路由。

## 指标是观察结果，不是效果宣称

`prevention stats` 接受可选 `--project` 与 `--issue` 过滤。所有计数都在 SQL 中根据当前数据库行计算。

- `by_kind` 按不同产物计数，`by_status` 按不同版本计数；多个 issue 关联不会放大产物总数。
- 验证通过率为“通过次数 / 全部运行次数”。零次运行时，两个计数和比率均为零。
- 每条已激活版本针对每个关联 issue 生成一条观察。`first_seen_to_activation_ms` 最小为零。
- 对激活时刻 `T`，前窗为 `[T-30*24h, T)`，后窗为 `(T, T+30*24h]`；恰好位于 `T` 的 occurrence 两个窗口都不计。
- `before_days` 与 `after_days` 按完整 24 小时计算并限制在 `0..30`。窗口不足时只展示有效天数和计数，不推断下降百分比。
- `recurrence_count` 统计 `T` 之后的**全部** occurrence，包括 30 天后窗之外的记录；它不能与 `after_count` 混用。
- `after_days < 30` 为 `observing`，满 30 天为 `mature`。只有成熟观察进入 `mature_protection_rate`，其值为其中 `after_count=0` 的比例；没有成熟观察时比率为 `null`，界面显示“暂无成熟记录”。
- `recurring_version_ids` 只包含当前 active 且有复发的版本。系统刻意不提供 `prevented_count`：未发生的错误不能被归因成该产物实测预防的数量。

保留策略和定向删除会影响后续指标，因为统计基于剩余的真实 occurrence。仅按连接器、仅按时间删除和保留策略不会静默删除 active 预防产物；项目删除会在预览和确认后移除该项目的预防链。备份包含全部四张预防表。

## 故障排查

| 现象                   | 检查什么                                                                  |
| ---------------------- | ------------------------------------------------------------------------- |
| 登记提示 issue 未解决  | 先解决或纠正 issue；预防登记只从已解决问题开始                            |
| 登记拒绝路径           | 使用仓库相对斜杠路径的普通文件；移除符号链接，大小不超过 1 MiB            |
| 验证报告内容已变化     | 文件不再匹配登记哈希；把变化后的内容登记为新版本再验证                    |
| 验证失败或命令退出非零 | 查看已脱敏且有界的证据；非零退出、启动失败和 120 秒超时都会记录为失败运行 |
| 激活提示最新验证未通过 | 针对当前精确哈希重新成功验证；旧通过记录不够                              |
| 激活拒绝 Git 状态      | 确保 `HEAD` 存在、产物已跟踪于 `HEAD`，且该路径的 `git status` 干净       |
| daemon 拒绝操作        | 迁移到 `0009`、重启 daemon，并使用包含 Unreleased 预防契约的构建          |
| 成熟保护率为空         | 还没有激活记录拥有完整 30 天后窗；这不代表 0% 或 100%                     |
| 复发数与后窗计数不同   | 复发数覆盖激活后全部时间，`after_count` 只覆盖 30 天窗口                  |

## 隐私与运维边界

产物正文和仓库绝对路径不进入 PostgreSQL。验证命令参数和有界 stdout/stderr 证据在存储前脱敏。环境元数据使用固定白名单，而不是转储进程环境变量。数据库转储仍包含全部已存储预防元数据和验证证据，因此应像保护数据库一样保护转储。

完整数据边界见 [privacy.zh-CN.md](privacy.zh-CN.md)，源码映射见 [features-and-implementation.zh-CN.md](features-and-implementation.zh-CN.md)。
