# batcher

基于 ffmpeg 的视频批处理工具，提供交互式终端界面（TUI）。支持视频转码、添加字幕、添加字体的任意组合——多个操作在同一条 ffmpeg 命令内一次完成，只处理一遍。

## 目录结构

```text
cmd/
  batcher/            TUI 入口
internal/
  ffmpeg/             核心逻辑：命令生成（batcher.go / combined.go）、并发执行（execute.go）、
                      进度事件（event.go）、服务封装（service.go）、参数定义（option.go）
  presets/            转码参数预设：内置预设（builtin.go）+ 用户自定义预设读写（presets.go）
  tui/                bubbletea 交互式界面（model.go 状态与事件、pages.go 页面逻辑、views.go 渲染）
```

## TUI

基于 [Bubble Tea v2](https://github.com/charmbracelet/bubbletea) 的交互式终端界面：

```bash
go run ./cmd/batcher
```

界面流程：

1. `space` 勾选操作（视频转码 / 添加字幕 / 添加字体，至少一项）。
2. 勾选了转码 → 转码参数页：选择内置预设或自定义预设（`Enter` 使用）、`d` 删除自定义预设、`a` 从内置预设复制一份微调，或选择"(手动输入参数)"进入表单手填。在表单页的"高级自定义参数"里按 `ctrl+s` 可把当前参数保存为自定义预设。
3. 勾选了字幕 → 字幕列表页：`a` 新增、`e` 编辑、`d` 删除，支持多条字幕；每条字幕可指定后缀、语言缩写（ISO 639-2 三字母码，如 `chi` / `eng` / `jpn`）、轨道标题。字幕文件按 `<视频名>.<后缀>` 或 `<视频名>.<标记>.<后缀>` 匹配（如 `1.mp4` 对应 `1.ass`、`1.chi.ass`、`1.eng.srt`；同一后缀有多条时用带标记的后缀管理，例如后缀填 `chi.ass` / `eng.ass`）。多个字幕流序号从 `input_sub_no` 起自动递增。
4. 主参数表单 → `Enter` 生成命令预览（相当于内置 dry-run，不会运行命令）→ `Enter` 开始执行，带实时进度条、耗时与 ffmpeg 输出日志。

组合逻辑：命令为 `ffmpeg -i <视频> [高级参数] [字幕输入×N] -map 0 -map 1 … -metadata:s:s:0 language=chi … [字体参数] [未开启转码时加 -c copy] <输出>`。

按键：`q`/`Ctrl+C` 退出；执行中 `q`/`esc` 中止批处理；结果页 `r` 重新执行、`b` 返回参数配置。

按 `p` 可随时进入"转码参数预设管理"：内置预设只读展示作参考，自定义预设支持新增、编辑、删除与排序（顺序即转码参数页的显示顺序）。

## 预设存储

- 内置预设：随程序内置（代码 `internal/presets/builtin.go`），只读，包含 H.264/H.265、NVIDIA 与 Apple VideoToolbox 硬件加速、无损封装等常用参数。
- 自定义预设：保存在 **CLI 可执行文件所在目录下的 `presets/` 文件夹**（`advance_presets.json`），保存时自动创建，随分发的可执行文件走，便于便携使用。
- 环境变量 `BATCHER_PRESETS_DIR` 可覆盖预设目录（开发、测试或特殊部署时使用）。

## 测试

```bash
go test ./internal/...
```

ffmpeg 相关测试会真实调用 ffmpeg 可执行文件（如 `-version`），需要本机已安装 ffmpeg；
输入文件均为测试内临时生成的占位文件（内部不校验媒体内容），无需手动准备 testdata。

ffmpeg 常用参数速查见 [docs/ffmpeg.md](docs/ffmpeg.md)。
