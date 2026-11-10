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

- 包划分（依赖单向 `tui → task → {command, media, ffmpeg}`，没有环）：`internal/command` 只读规划（配置、`Selection`、`BuildPlan`/`BatchPlan`/`Job`、`FormatCommand`），**规划不创建目录、不启动进程**；`internal/media` 负责扫描与路径校验（`CheckPaths`）；`internal/ffmpeg` 只管进程（`Runner` 启动单个 ffmpeg 并转成 `Event`）；`internal/task` 的 `Execute` 按顺序执行计划快照，**只有 `Runner` 真正起 ffmpeg 进程，输出目录也只在执行时创建**；`internal/testutil` 是各包测试共用的占位文件。`BatchPlan`/`Job` 保存预览与执行共用的快照，执行不得重扫目录。单一操作和组合操作共用 `internal/command/plan.go` 的构建器，所有输入必须在输出编码选项之前；转码+字幕+字体拼进同一条命令，不要拆成多条。默认禁止覆盖输出，新增字幕排在原字幕之前，避免依赖媒体探测计算字幕偏移。
- `internal/presets`：转码参数预设。`builtin.go` 是代码内置预设（只读）；用户预设存 **可执行文件所在目录下的 `presets/advance_presets.json`**（不是 ~/.config 也不是 ~/.batcher），保存时才创建目录。
- `internal/tui`：按职责分文件 —— `model.go`（状态/消息/事件分发、队列事件映射）、`panels.go`（各面板按键逻辑）、`modal.go`（弹窗：预设/字幕编辑与删除确认、帮助）、`fields.go`（表单字段构建与校验）、`views.go`（全部渲染）、`styles.go`（样式）。表单弹窗内 `enter` 在中间字段推进、末字段（含 bool）直接保存，`ctrl+s` 任意位置保存，`esc` 取消；确认类弹窗仍是 enter 确认。**没有 `phase` 状态机和向导式跳转**：布局是左右分栏（左侧 [1]-[5] 面板，右侧为焦点面板的内容），状态由 `focus`（当前面板）+ `editing`（表单输入态）+ `modal`（弹窗）+ `running`（当前批次执行中）+ `pausedBatch`（队列暂停等待 r/s/a 决策）组合表达。执行由 `internal/task.Queue` 驱动：`x` 入队当前配置（队列空闲自动开始），队列事件经 `queueEventMsg`/`initQueue` 的缓冲 channel 进入 Update 循环；**事件监听链必须常驻：只有 `Init()` 与 Update 的 `queueEventMsg` 分支挂 `listenEvents`，其他消息不得挂（会泄漏或断链，空闲时入队将永远不执行，有 `TestQueueEventsFlowWhenIdle` 拦截）**；运行期间重新生成的计划暂存 `pendingPlan`，批次结束后再应用（避免执行中文件列表下标错位）。按键分发顺序是固定优先级：弹窗 > 表单编辑 > 运行态 q/esc与面板键 > 全局键（1-5/tab/x/q） > 焦点面板；新增交互必须遵守这个顺序，编辑态和弹窗里的按键必须被吞掉，否则 `j/k/x/1-5` 会泄漏成全局快捷键。任务面板 [5] 内：入队后进入**队列视图**（`syncRoster` 由队列事件驱动重建）：左侧列表为主任务行（状态+进度概要）+ 可展开的子任务行（默认展开运行/暂停批次），`enter` 折叠/展开，`y` 复制光标子任务命令（批次行=全批命令），`f` 跳回执行中子任务；置顶命令块始终显示**光标所在子任务**的完整命令（与是否正在执行无关）；光标子任务的历史批次显示其错误尾部（Items.StderrTail）。修改配置（`schedulePlan`）即清空队列视图回到命令预览。暂停等待决策时计时冻结（`pausedAt`/`elapsedNow`），恢复执行扣除暂停时长；暂停时 `r` 重试 / `s` 跳过 / `a` 中止；`q`/`Esc` 取消当前批次（`queue.CancelCurrent`，队列继续下一个）。
- 命令预览是实时的：任何配置变化调 `schedulePlan` 防抖生成（`planMsg.seq` 丢弃过期结果）；执行期间同样生成，结果暂存 `pendingPlan`。改某个面板的配置时记得调 `schedulePlan`。
- 左侧面板布局：`panelHeights` 让未聚焦面板折叠为 2 行、聚焦面板占满剩余空间，五行内容行数总和必须等于 `bodyH()-10`（有回归测试 `TestPanelHeightsFillBody`）。侧栏列表一律走 `windowRows` 滚动窗口保证光标行可见。`drawBox` 手绘边框时任何样式串里的 `\n` 必须放在 `Render()` 之外，顶边 `fill = innerW - 标题宽`（不要多减），否则线框错位（有 `TestDrawBoxWidthsAligned`/`TestRenderRowsUniform` 拦截）。

## 预设相关的坑

- TUI 中预设列表顺序是 `内置 → 自定义 → 手动输入`，`presetCursor` 对这三段连续编号（见 `updatePresetsPanel`）。内置条目只读：删除/编辑/排序只作用于自定义段（下标偏移 `presetUserOffset()`）。
- 测试放预设文件用 `t.Setenv("BATCHER_PRESETS_DIR", t.TempDir())`（见 `internal/tui/model_test.go` 的 `isolatePresets`）；`internal/presets` 测试则替换 `executablePath` 包变量注入假 exe 路径。**不要让任何测试真正读写 exe 同目录的 presets/**。

## 测试约定

- 命令单元测试使用临时占位文件：`internal/testutil` 的 `NewFixture(t)` 在 `t.TempDir()` 自动生成；`internal/task/integration_test.go` 另外运行时生成微型真实媒体，用 ffprobe 校验组合结果。**不要新建 repo 内 testdata/，也不要手工 touch 占位文件**。期望值用 `testutil.Outputs`/`testutil.AttachCmd` helper 动态算，不要硬编码路径。
- `internal/ffmpeg` 的 `Runner` 测试和 `internal/task` 的执行测试会真实调用 ffmpeg（`-version`、故意失败的命令），本机必须装 ffmpeg。
- 断言用 testify（assert/require），与现有测试保持一致。

## 约定

- 界面文案、注释、错误信息一律中文；错误用 `fmt.Errorf + %w` 包裹。
- 新增 `VideoBatchOption` 字段需要同时改：`internal/command/option.go`（结构体 + `DefaultOption`）、`internal/tui/fields.go`（`fieldsFor` + `setStringOption`/`setIntOption` + placeholder）。
- 新增内置预设要评估：CPU 编码（x264/x265）、NVIDIA、Apple VideoToolbox、无损 `-c copy`；音频轨道用 `-c:a copy`。
