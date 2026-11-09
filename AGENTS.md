# batcher — Agent Notes

Go TUI 工具（Bubble Tea v2）：批量调用 ffmpeg 做转码/字幕封装/字体内嵌。没有 CLI 子命令、没有 GUI —— 唯一入口是 `cmd/batcher`，交互全部走 TUI。

## 常用命令

```bash
go build ./... && go vet ./...          # 构建 + 静态检查
go test ./internal/...                  # 全部测试（CI 同款，加 ./cmd/batcher/...）
go test ./internal/ffmpeg/... -run Test_videoBatch_GetConvertBatch   # 只跑一个测试
gofmt -l internal cmd                   # 格式检查；提交前对改动文件跑 gofmt -w
go run ./cmd/batcher                    # 本地启动 TUI（需终端，CI 无法运行）
```

没有 Taskfile（已移除），一切用原生 go 命令。发布走 `.goreleaser.yaml`（`cmd/batcher`），版本记录在 `CHANGELOG.md`。

## 结构与数据流

- `internal/ffmpeg`：核心包，不含 UI 和预设。**`Plan`/`Preview*` 只生成命令字符串、无副作用；只有 `ExecuteBatch` 真正起 ffmpeg 进程**。命令生成集中在 `batcher.go`（单一操作）和 `combined.go`（组合操作：转码+字幕+字体必须拼进同一条 ffmpeg 命令，不要拆成多条）。
- `internal/presets`：转码参数预设。`builtin.go` 是代码内置预设（只读）；用户预设存 **可执行文件所在目录下的 `presets/advance_presets.json`**（不是 ~/.config 也不是 ~/.batcher），保存时才创建目录。
- `internal/tui`：按职责分文件 —— `model.go`（状态/消息/事件分发）、`panels.go`（各面板按键逻辑）、`modal.go`（弹窗：预设/字幕编辑与删除确认）、`fields.go`（表单字段构建与校验）、`views.go`（全部渲染）、`styles.go`（样式）。**没有 `phase` 状态机和向导式跳转**：布局是左右分栏（左侧 [1]-[5] 面板，右侧为焦点面板的内容），状态由 `focus`（当前面板）+ `editing`（表单输入态）+ `modal`（弹窗）+ `running`（执行中）组合表达。按键分发顺序是固定优先级：弹窗 > 表单编辑 > 执行中查看 > 全局键（1-5/tab/表单中的 x/q）> 焦点面板；新增交互必须遵守这个顺序，编辑态和弹窗里的按键必须被吞掉，否则 `j/k/x/1-5` 会泄漏成全局快捷键。
- 命令预览是实时的：任何配置变化调 `schedulePlan` 防抖生成（`planMsg.seq` 丢弃过期结果）；执行期间锁定配置不生成。改某个面板的配置时记得调 `schedulePlan`。
- 左侧面板布局：`panelHeights` 让未聚焦面板折叠为 2 行、聚焦面板占满剩余空间，五行内容行数总和必须等于 `bodyH()-10`（有回归测试 `TestPanelHeightsFillBody`）。侧栏列表一律走 `windowRows` 滚动窗口保证光标行可见。`drawBox` 手绘边框时任何样式串里的 `\n` 必须放在 `Render()` 之外，顶边 `fill = innerW - 标题宽`（不要多减），否则线框错位（有 `TestDrawBoxWidthsAligned`/`TestRenderRowsUniform` 拦截）。

## 预设相关的坑

- TUI 中预设列表顺序是 `内置 → 自定义 → 手动输入`，`presetCursor` 对这三段连续编号（见 `updatePresetsPanel`）。内置条目只读：删除/编辑/排序只作用于自定义段（下标偏移 `presetUserOffset()`）。
- 测试放预设文件用 `t.Setenv("BATCHER_PRESETS_DIR", t.TempDir())`（见 `internal/tui/model_test.go` 的 `isolatePresets`）；`internal/presets` 测试则替换 `executablePath` 包变量注入假 exe 路径。**不要让任何测试真正读写 exe 同目录的 presets/**。

## 测试约定

- 媒体测试文件是临时占位文件（仅校验按文件名/后缀生成的命令，不校验内容）：`internal/ffmpeg/ffmpeg_test.go` 的 `newFixture(t)` 在 `t.TempDir()` 自动生成，**不要新建 repo 内 testdata/，也不要手工 touch 占位文件**。期望值用 `outputs`/`attachCmd` helper 动态算，不要硬编码路径。
- `ExecuteBatch` 系列测试会真实调用 ffmpeg（`-version`、故意失败的命令），本机必须装 ffmpeg。
- 断言用 testify（assert/require），与现有测试保持一致。

## 约定

- 界面文案、注释、错误信息一律中文；错误用 `fmt.Errorf + %w` 包裹。
- 新增 `VideoBatchOption` 字段需要同时改：`option.go`（结构体 + `DefaultOption`）、`internal/tui/fields.go`（`fieldsFor` + `setStringOption`/`setIntOption` + placeholder）。
- 新增内置预设要评估：CPU 编码（x264/x265）、NVIDIA、Apple VideoToolbox、无损 `-c copy`；音频轨道用 `-c:a copy`。
