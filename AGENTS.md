# batcher — Agent Notes

Go TUI 工具（Bubble Tea v2）：批量调用 ffmpeg 做转码/字幕封装/字体内嵌。没有 CLI 子命令、没有 GUI —— 唯一入口是 `cmd/batcher`，交互全部走 TUI。

## 常用命令

```bash
go build ./... && go vet ./...          # 构建 + 静态检查
go test ./internal/...                  # 全部测试（CI 同款，加 ./cmd/batcher/...）
go test ./internal/command/... -run TestBuildPlanConvertCommands   # 只跑一个测试
gofmt -l internal cmd                   # 格式检查；提交前对改动文件跑 gofmt -w
go run ./cmd/batcher                    # 本地启动 TUI（需终端，CI 无法运行）
```

没有 Taskfile（已移除），一切用原生 go 命令。发布走 `.goreleaser.yaml`（`cmd/batcher`），版本记录在 `CHANGELOG.md`。

## 结构与数据流

- 包划分（依赖单向 `tui → {task, store} → {command, media, ffmpeg, presets}`，`task` 不导入 `store`，没有环）：`internal/command` 只读规划（配置、`Selection`、`BuildPlan`/`BatchPlan`/`Job`、`FormatCommand`），**规划不创建目录、不启动进程**；`internal/media` 负责扫描与路径校验（`CheckPaths`）；`internal/ffmpeg` 只管进程（`Runner` 启动单个 ffmpeg 并转成 `Event`）；`internal/task` 的队列按顺序执行计划快照，**只有 `Runner` 真正起 ffmpeg 进程，输出目录也只在执行时创建**；`internal/testutil` 是各包测试共用的占位文件。`BatchPlan`/`Job` 保存预览与执行共用的快照，执行不得重扫目录。单一操作和组合操作共用 `internal/command/plan.go` 的构建器，所有输入必须在输出编码选项之前；转码+字幕+字体拼进同一条命令，不要拆成多条。默认禁止覆盖输出，新增字幕排在原字幕之前，避免依赖媒体探测计算字幕偏移。
- `internal/store`：`go.etcd.io/bbolt` 数据库 `batcher.db`，放在**可执行文件所在目录**（`store.Dir()`，可用 `BATCHER_DATA_DIR` 覆盖）。桶为 `tasks`、`presets`、`meta`。`Open` 首次打开时把旧的 `presets/advance_presets.json` 导入一次（`meta.presetsImported`）；`OpenDir` 不导入。`task` 通过 `BatchStore` + `BindStore` 存任务，`presets` 通过 `Store` + `Bind` 存自定义预设。入队 `Save` 不创建输出目录。`d` 删除任务时先记 `forget`，队列移除事件里再删库，避免随后的 `Save` 把记录写回去。
- `internal/presets`：`builtin.go` 是代码内置预设（只读）。自定义预设的主存储是上面的 bbolt，不再写 `advance_presets.json`。`LoadFrom` / `SaveTo` / `Dir`（`<exe>/presets`，可用 `BATCHER_PRESETS_DIR` 覆盖）只服务旧文件导入和文件层测试。
- `internal/tui`：按职责分文件 —— `model.go`（状态/消息/事件分发、队列事件映射）、`panels.go`（任务列表按键）、`modal.go`（弹窗）、`task_modal.go`（创建任务与预设列表）、`fields.go`、`views.go`、`styles.go`、`run.go`（打开数据库并绑定）。主界面是左右两栏：左侧任务列表，右侧当前任务的状态、命令和日志；宽度小于 90 时上下叠成一列。**没有五面板向导，也没有 `phase` 状态机**。主界面 `a` 打开**一个**新建任务弹窗，转码/字幕/字体、路径、预设、高级参数和字幕组都在同一张表单里，`enter` 只在字段间前进，末字段或 `ctrl+s` 直接创建；`ctrl+n` 加一组字幕，`ctrl+d` 删当前组。主界面 `p` 打开转码预设管理：内置只读，`a` 在内置项上复制、在自定义项上新增，`e` 编辑，`d` 确认删除，`J`/`K` 排序；保存或取消后回到列表。执行中或暂停时 `a` 被拒绝。表单弹窗内 `enter` 在中间字段推进、末字段直接保存，`ctrl+s` 任意位置保存，`esc` 取消；确认类弹窗仍是 enter 确认。状态由 `modal` + `editing` + `running` + `pausedBatch` 组合表达。执行由 `internal/task.Queue` 驱动：创建成功后入队（队列空闲自动开始），队列事件经 `queueEventMsg`/`initQueue` 的缓冲 channel 进入 Update 循环；**事件监听链必须常驻：只有 `Init()` 与 Update 的 `queueEventMsg` 分支挂 `listenEvents`，其他消息不得挂（会泄漏或断链，空闲时入队将永远不执行，有 `TestQueueEventsFlowWhenIdle` 拦截）**。按键分发顺序是固定优先级：弹窗 > 表单编辑 > 输出查看 > 运行/暂停时的 `q`/`esc`/`a` > 全局键（`a`/`p`/`q`/`?`）> 任务列表；弹窗和编辑态必须吞掉按键。列表由 `syncRoster` 合并队列中的批次和库里的历史任务：主任务行可展开子任务和每一次尝试，`enter` 折叠/展开，`y` 复制光标命令，`f` 跳回执行中的子任务；右侧始终显示光标所在子任务的完整命令。暂停时计时冻结。队列操作键按光标行分发（`runTaskKeys`）：暂停时 `r` 重试 / `s` 跳过 / `x` 中止；批次行 `r` 重试整个任务（已结束批次走 `RetryBatch`，不在队列里的历史任务走 `Requeue`），`d` 从列表和数据库删除（运行中拒绝）。`q`/`Esc` 在暂停时中止当前批次，执行中取消当前批次，空闲时 `q` 退出。**执行中（含暂停）不得再创建或入队**。
- 侧栏列表走 `windowRows` 保证光标行可见。`drawBox` 手绘边框时任何样式串里的 `\n` 必须放在 `Render()` 之外，顶边 `fill = innerW - 标题宽`（不要多减），否则线框错位（有 `TestDrawBoxWidthsAligned`/`TestRenderRowsUniform` 拦截）。结束后的耗时用已记下的 `m.elapsed`，不要用 `elapsedNow()`。

## 预设相关的坑

- 预设管理列表只有 `内置 → 自定义`（`presetManageCount`）。创建任务弹窗里的预设字段才有「手动输入」。内置条目只读：删除/编辑/排序只作用于自定义段（下标偏移 `presetUserOffset()`）。在内置项上按 `a` 是复制，不是打开空白表单。
- TUI 测试用 `isolatePresets`：`store.OpenDir(t.TempDir())` 后 `task.BindStore` 与 `presets.Bind`，结束时关闭并解绑。`internal/store` 用 `BATCHER_DATA_DIR`；`internal/presets` 的文件测试替换 `executablePath`。**不要让任何测试读写可执行文件旁边的 `batcher.db` 或 `presets/`。** `task` 包测试用包内 `memStore`，不要为了测 bbolt 去导入 `internal/store`（会形成 import cycle）。

## 测试约定

- 命令单元测试使用临时占位文件：`internal/testutil` 的 `NewFixture(t)` 在 `t.TempDir()` 自动生成；`internal/task/integration_test.go` 另外运行时生成微型真实媒体，用 ffprobe 校验组合结果。**不要新建 repo 内 testdata/，也不要手工 touch 占位文件**。期望值用 `testutil.Outputs`/`testutil.AttachCmd` helper 动态算，不要硬编码路径。
- `internal/ffmpeg` 的 `Runner` 测试和 `internal/task` 的执行测试会真实调用 ffmpeg（`-version`、故意失败的命令），本机必须装 ffmpeg。
- 断言用 testify（assert/require），与现有测试保持一致。

## 约定

- 界面文案、注释、错误信息一律中文；错误用 `fmt.Errorf + %w` 包裹。
- 新增 `VideoBatchOption` 字段需要同时改：`internal/command/option.go`（结构体 + `DefaultOption`）、`internal/tui/fields.go`（`fieldsFor` + `setStringOption`/`setIntOption` + placeholder）。
- 新增内置预设要评估：CPU 编码（x264/x265）、NVIDIA、Apple VideoToolbox、无损 `-c copy`；音频轨道用 `-c:a copy`。
